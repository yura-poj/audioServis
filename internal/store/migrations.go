package store

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/001_initial.sql
var initialSchema string

//go:embed migrations/002_listening_history.sql
var historySchema string

//go:embed migrations/003_track_embeddings.sql
var embeddingSchema string

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
		for i, schema := range []string{initialSchema, historySchema, embeddingSchema} {
			version := i + 1
			var applied bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&applied); err != nil {
				return err
			}
			if applied {
				continue
			}
			if _, err := tx.Exec(ctx, schema); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
				return err
			}
		}
		return nil
	})
}
