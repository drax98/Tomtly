package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// userLock serialises everything that can change who the enabled admins are, so the last-admin check can't race.
const userLock = 7266302

const userColumns = `id, username, hash, role, disabled, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.Hash, &u.Role, &u.Disabled, &u.Created)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// pgCode maps the two constraint failures callers care about.
func pgCode(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return ErrExists
		case "23503":
			return ErrNotFound
		}
	}
	return err
}

const insertUser = `INSERT INTO users (` + userColumns + `) VALUES ($1, $2, $3, $4, $5, $6)`

func (p *Postgres) CreateFirstUser(ctx context.Context, u User) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after commit this is a no-op
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userLock); err != nil {
		return err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return ErrExists
	}
	if _, err := tx.Exec(ctx, insertUser, u.ID, u.Username, u.Hash, u.Role, u.Disabled, u.Created); err != nil {
		return pgCode(err)
	}
	return tx.Commit(ctx)
}

func (p *Postgres) CreateUser(ctx context.Context, u User) error {
	_, err := p.pool.Exec(ctx, insertUser, u.ID, u.Username, u.Hash, u.Role, u.Disabled, u.Created)
	return pgCode(err)
}

func (p *Postgres) UserByName(ctx context.Context, username string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE lower(username) = lower($1)`, username))
}

func (p *Postgres) UserByID(ctx context.Context, id string) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
}

func (p *Postgres) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := p.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY created_at, username`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (User, error) { return scanUser(row) })
}

func (p *Postgres) UpdateUser(ctx context.Context, id string, c UserChange) (User, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after commit this is a no-op
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userLock); err != nil {
		return User{}, err
	}
	u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		return User{}, err
	}
	wasAdmin := u.Role == "admin" && !u.Disabled
	if c.Role != nil {
		u.Role = *c.Role
	}
	if c.Disabled != nil {
		u.Disabled = *c.Disabled
	}
	if c.Hash != nil {
		u.Hash = *c.Hash
	}
	if wasAdmin && (u.Role != "admin" || u.Disabled) {
		if err := requireOtherAdmin(ctx, tx, id); err != nil {
			return User{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET role = $2, disabled = $3, hash = $4 WHERE id = $1`, id, u.Role, u.Disabled, u.Hash); err != nil {
		return User{}, err
	}
	if c.Role != nil || c.Disabled != nil || c.Hash != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND hash <> $2`, id, c.KeepSession); err != nil {
			return User{}, err
		}
	}
	if c.DropTokens {
		if _, err := tx.Exec(ctx, `DELETE FROM api_tokens WHERE user_id = $1`, id); err != nil {
			return User{}, err
		}
	}
	return u, tx.Commit(ctx)
}

func requireOtherAdmin(ctx context.Context, tx pgx.Tx, id string) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE id <> $1 AND role = 'admin' AND NOT disabled`, id).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

func (p *Postgres) DeleteUser(ctx context.Context, id string) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after commit this is a no-op
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userLock); err != nil {
		return err
	}
	u, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id))
	if err != nil {
		return err
	}
	if u.Role == "admin" && !u.Disabled {
		if err := requireOtherAdmin(ctx, tx, id); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil { // sessions and tokens cascade
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) CreateSession(ctx context.Context, hash, userID string, now time.Time) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO sessions (hash, user_id, created_at, last_seen) VALUES ($1, $2, $3, $3)`, hash, userID, now)
	return pgCode(err)
}

func (p *Postgres) SessionUser(ctx context.Context, hash string, now time.Time, idle, absolute time.Duration) (User, error) {
	return scanUser(p.pool.QueryRow(ctx, `UPDATE sessions s SET last_seen = $2 FROM users u
		WHERE s.hash = $1 AND u.id = s.user_id AND s.created_at > $3 AND s.last_seen > $4
		RETURNING u.id, u.username, u.hash, u.role, u.disabled, u.created_at`, hash, now, now.Add(-absolute), now.Add(-idle)))
}

func (p *Postgres) DeleteSession(ctx context.Context, hash string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE hash = $1`, hash)
	return err
}

func (p *Postgres) DeleteExpiredSessions(ctx context.Context, now time.Time, idle, absolute time.Duration) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM sessions WHERE created_at <= $1 OR last_seen <= $2`, now.Add(-absolute), now.Add(-idle))
	return err
}

func (p *Postgres) CreateToken(ctx context.Context, t Token, hash string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO api_tokens (id, hash, user_id, name, role, created_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, hash, t.UserID, t.Name, t.Role, t.Created)
	return pgCode(err)
}

func scanToken(row pgx.Row, user *User) (Token, error) {
	var t Token
	var used *time.Time
	dest := []any{&t.ID, &t.UserID, &t.Owner, &t.Name, &t.Role, &t.Created, &used}
	if user != nil {
		dest = append(dest, &user.ID, &user.Username, &user.Hash, &user.Role, &user.Disabled, &user.Created)
	}
	if err := row.Scan(dest...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Token{}, ErrNotFound
		}
		return Token{}, err
	}
	if used != nil {
		t.LastUsed = *used
	}
	return t, nil
}

func (p *Postgres) TokenUser(ctx context.Context, hash string, now time.Time) (Token, User, error) {
	var u User
	t, err := scanToken(p.pool.QueryRow(ctx, `UPDATE api_tokens t SET last_used = $2 FROM users u WHERE t.hash = $1 AND u.id = t.user_id
		RETURNING t.id, t.user_id, u.username, t.name, t.role, t.created_at, t.last_used,
		u.id, u.username, u.hash, u.role, u.disabled, u.created_at`, hash, now), &u)
	return t, u, err
}

func (p *Postgres) ListTokens(ctx context.Context, userID string) ([]Token, error) {
	rows, err := p.pool.Query(ctx, `SELECT t.id, t.user_id, u.username, t.name, t.role, t.created_at, t.last_used
		FROM api_tokens t JOIN users u ON u.id = t.user_id WHERE $1 = '' OR t.user_id = $1 ORDER BY t.seq`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Token, error) { return scanToken(row, nil) })
}

func (p *Postgres) DeleteToken(ctx context.Context, id, userID string) error {
	tag, err := p.pool.Exec(ctx, `DELETE FROM api_tokens WHERE id = $1 AND ($2 = '' OR user_id = $2)`, id, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) AppendAudit(ctx context.Context, e AuditEntry) error {
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO audit (time, username, action, detail, ip) VALUES ($1, $2, $3, $4, $5)`, e.Time, e.User, e.Action, e.Detail, e.IP)
	batch.Queue(`DELETE FROM audit WHERE id <= (SELECT id FROM audit ORDER BY id DESC OFFSET $1 LIMIT 1)`, maxAudit)
	return p.runBatch(ctx, batch)
}

func (p *Postgres) RecentAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := p.pool.Query(ctx, `SELECT time, username, action, detail, ip FROM audit ORDER BY id DESC LIMIT $1`, max(limit, 0))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[AuditEntry])
}
