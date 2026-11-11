// Package pgmigrate applies a service's SQL migrations, the same way for Tomtly and the marketplace.
package pgmigrate

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Apply runs migrations/NNN_name.sql from files in name order, all in one transaction, recording each in table. The
// advisory lock makes a second starting instance wait for the first, and then see its migrations as applied. Each
// service keeps its own lock and table, so they can share a database without seeing each other's migrations.
func Apply(ctx context.Context, pool *pgxpool.Pool, files fs.FS, lock int64, table string) error {
	entries, err := fs.ReadDir(files, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // after commit this is a no-op
	quoted := pgx.Identifier{table}.Sanitize()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lock); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS `+quoted+` (name text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT name FROM `+quoted)
	if err != nil {
		return err
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	for _, name := range names {
		if slices.Contains(applied, name) {
			continue
		}
		sql, err := fs.ReadFile(files, "migrations/"+name)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO `+quoted+` (name) VALUES ($1)`, name); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
