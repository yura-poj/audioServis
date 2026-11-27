package store

import (
	"context"
	"errors"
	"time"

	"audioservice/internal/wave"

	"github.com/jackc/pgx/v5"
)

var ErrWavePending = errors.New("wave analysis pending")
var ErrWaveFailed = errors.New("wave analysis failed")

func (s *Store) NextWave(ctx context.Context, user, current string, excluded map[string]bool, now time.Time) (Track, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Track{}, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, "SELECT t.id::text,e.status,e.vector FROM tracks t JOIN track_embeddings e ON e.track_id=t.id WHERE t.user_id=$1", user)
	if err != nil {
		return Track{}, err
	}
	defer rows.Close()
	var catalog []wave.Candidate
	found := current == ""
	var pending, failed bool
	for rows.Next() {
		var c wave.Candidate
		var status string
		if err := rows.Scan(&c.ID, &status, &c.Vector); err != nil {
			return Track{}, err
		}
		if c.ID == current {
			found = true
			if status == "pending" {
				return Track{}, ErrWavePending
			}
			if status == "failed" {
				return Track{}, ErrWaveFailed
			}
		}
		if status == "ready" {
			catalog = append(catalog, c)
		} else if !excluded[c.ID] {
			pending = pending || status == "pending"
			failed = failed || status == "failed"
		}
	}
	if err := rows.Err(); err != nil {
		return Track{}, err
	}
	if !found {
		return Track{}, ErrNotFound
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	rows, err = tx.Query(ctx, `
        SELECT h.track_id::text,
               CASE WHEN outcome='completed' AND start_reason='selection' THEN 2
                    WHEN outcome='completed' THEN 1 ELSE -1 END
        FROM listening_history h
        JOIN track_embeddings e ON e.track_id=h.track_id AND e.status='ready'
        WHERE user_id=$1 AND started_at BETWEEN $2 AND $3
          AND (outcome='completed' OR (outcome='skipped' AND listened_ms < duration_ms/4.0))`, user, start, now)
	if err != nil {
		return Track{}, err
	}
	defer rows.Close()
	var history []wave.Signal
	for rows.Next() {
		var h wave.Signal
		var w int
		if err := rows.Scan(&h.ID, &w); err != nil {
			return Track{}, err
		}
		h.Weight = float64(w)
		history = append(history, h)
	}
	if err := rows.Err(); err != nil {
		return Track{}, err
	}
	id := wave.Choose(catalog, current, excluded, history)
	if id == "" {
		if pending {
			return Track{}, ErrWavePending
		}
		if failed {
			return Track{}, ErrWaveFailed
		}
		return Track{}, nil
	}
	rows, err = tx.Query(ctx, "SELECT "+trackColumns+" FROM tracks WHERE user_id=$1 AND id=$2", user, id)
	if err != nil {
		return Track{}, err
	}
	return pgx.CollectExactlyOneRow(rows, trackFromRow)
}

func (s *Store) PendingEmbedding(ctx context.Context) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, "SELECT track_id::text FROM track_embeddings WHERE status='pending' ORDER BY track_id LIMIT 1").Scan(&id)
	return id, err
}
func (s *Store) SaveEmbedding(ctx context.Context, id string, v []byte) error {
	status := "ready"
	if v == nil {
		status = "failed"
	}
	_, err := s.pool.Exec(ctx, "UPDATE track_embeddings SET status=$2,vector=$3 WHERE track_id=$1 AND status='pending'", id, status, v)
	return err
}
