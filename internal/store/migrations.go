package store

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/001_initial.sql
var initialSchema string

func (s *Store) Migrate(ctx context.Context) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(487398011)`); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version integer PRIMARY KEY
			)
		`)
		if err != nil {
			return err
		}
		var applied bool
		err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = 1)`).Scan(&applied)
		if err != nil || applied {
			return err
		}
		if _, err = tx.Exec(ctx, initialSchema); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES (1)`)
		return err
	})
}
