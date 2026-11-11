package store

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"tomtly/internal/pgmigrate"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

const (
	maxEvents     = 10000
	maxViolations = 1000
)

// Postgres is a Store in a PostgreSQL database.
type Postgres struct {
	pool *pgxpool.Pool
}

// Open connects, then applies any migrations that haven't run. It fails fast if the database isn't reachable.
func Open(ctx context.Context, databaseURL string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("DATABASE_URL is not a valid connection URL") // the parse error would quote the password
	}
	cfg.MaxConns = 4
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	// A stuck query can't hold a connection forever, even if a caller forgot its own timeout.
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

func (p *Postgres) Close() { p.pool.Close() }

// migrate applies this service's migrations; the lock and table are this service's own, and never change.
func (p *Postgres) migrate(ctx context.Context) error {
	return pgmigrate.Apply(ctx, p.pool, migrationFiles, 7266301, "schema_migrations")
}

func (p *Postgres) ScriptsLive(ctx context.Context) (map[string]bool, error) {
	rows, err := p.pool.Query(ctx, `SELECT name, live FROM script_live`)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	var name string
	var live bool
	_, err = pgx.ForEachRow(rows, []any{&name, &live}, func() error { out[name] = live; return nil })
	return out, err
}

func (p *Postgres) SetScriptLive(ctx context.Context, name string, live bool) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO script_live (name, live) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET live = $2`, name, live)
	return err
}

func (p *Postgres) ScriptGrants(ctx context.Context) (map[string][]string, error) {
	rows, err := p.pool.Query(ctx, `SELECT script, key FROM script_grants ORDER BY script, key`)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	var script, key string
	_, err = pgx.ForEachRow(rows, []any{&script, &key}, func() error { out[script] = append(out[script], key); return nil })
	return out, err
}

func (p *Postgres) SetScriptGrants(ctx context.Context, script string, keys []string, granted bool) error {
	query := `DELETE FROM script_grants WHERE script = $1 AND key = ANY($2)`
	if granted {
		query = `INSERT INTO script_grants (script, key) SELECT $1, unnest($2::text[]) ON CONFLICT DO NOTHING`
	}
	_, err := p.pool.Exec(ctx, query, script, keys)
	return err
}

func (p *Postgres) AppendEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, e := range events {
		batch.Queue(`INSERT INTO events (time, script, kind, message) VALUES ($1, $2, $3, $4)`, e.Time, e.Script, e.Kind, e.Message)
	}
	// Keeps the newest maxEvents: finds the id just past them with one index scan of maxEvents+1 rows, which is
	// bounded however long the table once was, and deletes everything at or below it.
	batch.Queue(`DELETE FROM events WHERE id <= (SELECT id FROM events ORDER BY id DESC OFFSET $1 LIMIT 1)`, maxEvents)
	return p.runBatch(ctx, batch)
}

// runBatch sends the statements in one transaction.
func (p *Postgres) runBatch(ctx context.Context, batch *pgx.Batch) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after commit this is a no-op
	results := tx.SendBatch(ctx, batch)
	for range batch.Len() {
		if _, err := results.Exec(); err != nil {
			results.Close()
			return err
		}
	}
	if err := results.Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (p *Postgres) RecentEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := p.pool.Query(ctx, `SELECT time, script, kind, message FROM events ORDER BY id DESC LIMIT $1`, max(limit, 0))
	if err != nil {
		return nil, err
	}
	events, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Event])
	slices.Reverse(events)
	return events, err
}

func (p *Postgres) AddedExtensions(ctx context.Context) ([]AddedExtension, error) {
	rows, err := p.pool.Query(ctx, `SELECT source, settings FROM added_extensions ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[AddedExtension])
}

// SaveAddedExtension takes a fresh seq on replace, so the order matches Memory, which moves a replaced one to the end.
func (p *Postgres) SaveAddedExtension(ctx context.Context, extension AddedExtension) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO added_extensions (source, settings) VALUES ($1, $2)
		ON CONFLICT (source) DO UPDATE SET settings = $2, seq = nextval(pg_get_serial_sequence('added_extensions', 'seq'))`,
		extension.Source, extension.Settings)
	return err
}

func (p *Postgres) DeleteAddedExtension(ctx context.Context, source string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM added_extensions WHERE source = $1`, source)
	return err
}

func (p *Postgres) Grants(ctx context.Context, extension string) ([]string, error) {
	var grants []string
	err := p.pool.QueryRow(ctx, `SELECT grants FROM grants WHERE extension = $1`, extension).Scan(&grants)
	if errors.Is(err, pgx.ErrNoRows) {
		return []string{}, nil
	}
	return grants, err
}

func (p *Postgres) SetGrants(ctx context.Context, extension string, grants []string) error {
	if len(grants) == 0 {
		_, err := p.pool.Exec(ctx, `DELETE FROM grants WHERE extension = $1`, extension)
		return err
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO grants (extension, grants) VALUES ($1, $2) ON CONFLICT (extension) DO UPDATE SET grants = $2`, extension, grants)
	return err
}

func (p *Postgres) WebhookTokenHash(ctx context.Context, extension string) (string, error) {
	var hash string
	err := p.pool.QueryRow(ctx, `SELECT hash FROM webhook_tokens WHERE extension = $1`, extension).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (p *Postgres) SetWebhookTokenHash(ctx context.Context, extension, hash string) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO webhook_tokens (extension, hash) VALUES ($1, $2) ON CONFLICT (extension) DO UPDATE SET hash = $2`, extension, hash)
	return err
}

func (p *Postgres) DeleteWebhookToken(ctx context.Context, extension string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM webhook_tokens WHERE extension = $1`, extension)
	return err
}

func (p *Postgres) RecordViolation(ctx context.Context, v Violation) error {
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO violations (time, extension, kind, detail) VALUES ($1, $2, $3, $4)`, v.Time, v.Extension, v.Kind, v.Detail)
	// Same trick as events, per extension, using the (extension, id) index.
	batch.Queue(`DELETE FROM violations WHERE extension = $1 AND id <=
		(SELECT id FROM violations WHERE extension = $1 ORDER BY id DESC OFFSET $2 LIMIT 1)`, v.Extension, maxViolations)
	return p.runBatch(ctx, batch)
}

func (p *Postgres) Violations(ctx context.Context, extension string, limit int) ([]Violation, error) {
	rows, err := p.pool.Query(ctx, `SELECT time, extension, kind, detail FROM violations WHERE extension = $1 ORDER BY id DESC LIMIT $2`, extension, max(limit, 0))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Violation])
}

func (p *Postgres) ClearViolations(ctx context.Context, extension string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM violations WHERE extension = $1`, extension)
	return err
}

func (p *Postgres) RemovedExtensions(ctx context.Context) ([]RemovedExtension, error) {
	rows, err := p.pool.Query(ctx, `SELECT source, id, name, kind FROM removed_extensions ORDER BY source`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[RemovedExtension])
}

func (p *Postgres) AddRemovedExtension(ctx context.Context, e RemovedExtension) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO removed_extensions (source, id, name, kind) VALUES ($1, $2, $3, $4)
		ON CONFLICT (source) DO UPDATE SET id = EXCLUDED.id, name = EXCLUDED.name, kind = EXCLUDED.kind`, e.Source, e.ID, e.Name, e.Kind)
	return err
}

func (p *Postgres) DeleteRemovedExtension(ctx context.Context, source string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM removed_extensions WHERE source = $1`, source)
	return err
}

func (p *Postgres) RecordActivity(ctx context.Context, a Activity) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO extension_activity (extension, kind, destination, count, first_seen, last_seen) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (extension, kind, destination) DO UPDATE SET count = extension_activity.count + EXCLUDED.count, last_seen = GREATEST(extension_activity.last_seen, EXCLUDED.last_seen)`,
		a.Extension, a.Kind, a.Destination, a.Count, a.First, a.Last)
	return err
}

func (p *Postgres) Activity(ctx context.Context, extension string, limit int) ([]Activity, error) {
	rows, err := p.pool.Query(ctx, `SELECT extension, kind, destination, count, first_seen, last_seen FROM extension_activity WHERE extension = $1 ORDER BY last_seen DESC, destination LIMIT $2`, extension, max(limit, 0))
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Activity])
}

func (p *Postgres) DeleteActivity(ctx context.Context, extension string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM extension_activity WHERE extension = $1`, extension)
	return err
}

func (p *Postgres) RemovedDevices(ctx context.Context) ([]RemovedDevice, error) {
	rows, err := p.pool.Query(ctx, `SELECT id, extension, name, product FROM removed_devices ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[RemovedDevice])
}

func (p *Postgres) AddRemovedDevice(ctx context.Context, d RemovedDevice) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO removed_devices (id, extension, name, product) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET extension = $2, name = $3, product = $4`, d.ID, d.Extension, d.Name, d.Product)
	return err
}

func (p *Postgres) DeleteRemovedDevice(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM removed_devices WHERE id = $1`, id)
	return err
}

func (p *Postgres) ExtensionStorage(ctx context.Context, extension string) (map[string]json.RawMessage, error) {
	rows, err := p.pool.Query(ctx, `SELECT key, value FROM extension_storage WHERE extension = $1`, extension)
	if err != nil {
		return nil, err
	}
	out := map[string]json.RawMessage{}
	var key string
	var value json.RawMessage
	_, err = pgx.ForEachRow(rows, []any{&key, &value}, func() error { out[key] = slices.Clone(value); return nil })
	return out, err
}

func (p *Postgres) SetExtensionValue(ctx context.Context, extension, key string, value json.RawMessage) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO extension_storage (extension, key, value) VALUES ($1, $2, $3)
		ON CONFLICT (extension, key) DO UPDATE SET value = $3`, extension, key, value)
	return err
}

func (p *Postgres) DeleteExtensionValue(ctx context.Context, extension, key string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM extension_storage WHERE extension = $1 AND key = $2`, extension, key)
	return err
}

func (p *Postgres) DeleteExtensionStorage(ctx context.Context, extension string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM extension_storage WHERE extension = $1`, extension)
	return err
}

func (p *Postgres) InstallKey(ctx context.Context, candidate []byte) ([]byte, error) {
	if _, err := p.pool.Exec(ctx, `INSERT INTO install_key (key) VALUES ($1) ON CONFLICT (only_row) DO NOTHING`, candidate); err != nil {
		return nil, err
	}
	var key []byte
	err := p.pool.QueryRow(ctx, `SELECT key FROM install_key`).Scan(&key)
	return key, err
}

func (p *Postgres) MarketState(ctx context.Context) (MarketState, error) {
	var state MarketState
	err := p.pool.QueryRow(ctx, `SELECT newest, newest_signed, revoked FROM market_state`).Scan(&state.Newest, &state.NewestSigned, &state.Revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return MarketState{}, nil
	}
	return state, err
}

func (p *Postgres) SaveMarketState(ctx context.Context, state MarketState) error {
	_, err := p.pool.Exec(ctx, `INSERT INTO market_state (newest, newest_signed, revoked) VALUES ($1, $2, $3)
		ON CONFLICT (only_row) DO UPDATE SET newest = $1, newest_signed = $2, revoked = $3`, state.Newest, state.NewestSigned, string(state.Revoked))
	return err
}

func (p *Postgres) SavePackage(ctx context.Context, key string, archive []byte) error {
	if archive == nil {
		archive = []byte{}
	}
	_, err := p.pool.Exec(ctx, `INSERT INTO packages (key, archive) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET archive = $2`, key, archive)
	return err
}

func (p *Postgres) Package(ctx context.Context, key string) ([]byte, error) {
	var archive []byte
	err := p.pool.QueryRow(ctx, `SELECT archive FROM packages WHERE key = $1`, key).Scan(&archive)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	return archive, err
}

func (p *Postgres) DeletePackage(ctx context.Context, key string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM packages WHERE key = $1`, key)
	return err
}
