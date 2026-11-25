package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrPlayConflict = errors.New("playback identity conflict")

type PlaySnapshot struct {
	TrackID        string    `json:"track_id"`
	QueueID        string    `json:"queue_id"`
	Sequence       int64     `json:"sequence"`
	PreviousPlayID *string   `json:"previous_play_id"`
	StartedAt      time.Time `json:"started_at"`
	ListenedMS     int64     `json:"listened_ms"`
	DurationMS     *int64    `json:"duration_ms"`
	StartReason    string    `json:"start_reason"`
	Outcome        string    `json:"outcome"`
}

type Play struct {
	ID string `json:"id"`
	PlaySnapshot
}

type ListeningStats struct {
	TrackID     string      `json:"track_id"`
	Starts      int64       `json:"starts"`
	Completions int64       `json:"completions"`
	ListenedMS  json.Number `json:"listened_ms"`
}

func (s *Store) RecordPlay(ctx context.Context, user, id string, p PlaySnapshot) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT true FROM tracks WHERE user_id=$1 AND id=$2 FOR KEY SHARE`, user, p.TrackID).Scan(&exists); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO listening_history AS h
				(user_id,id,track_id,queue_id,sequence,previous_play_id,started_at,listened_ms,duration_ms,start_reason,outcome)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (user_id,id) DO UPDATE SET
				listened_ms=CASE WHEN h.outcome='in_progress' THEN greatest(h.listened_ms,excluded.listened_ms) ELSE h.listened_ms END,
				duration_ms=coalesce(h.duration_ms,excluded.duration_ms),
				outcome=CASE WHEN h.outcome='in_progress' THEN excluded.outcome ELSE h.outcome END
			WHERE (h.track_id,h.queue_id,h.sequence,h.started_at,h.start_reason) =
			      (excluded.track_id,excluded.queue_id,excluded.sequence,excluded.started_at,excluded.start_reason)
			  AND h.previous_play_id IS NOT DISTINCT FROM excluded.previous_play_id`,
			user, id, p.TrackID, p.QueueID, p.Sequence, p.PreviousPlayID, p.StartedAt, p.ListenedMS, p.DurationMS, p.StartReason, p.Outcome)
		var dbErr *pgconn.PgError
		if errors.As(err, &dbErr) && dbErr.ConstraintName == "listening_history_queue_position" {
			return ErrPlayConflict
		}
		if err == nil && tag.RowsAffected() == 0 {
			return ErrPlayConflict
		}
		return err
	})
}

func (s *Store) History(ctx context.Context, user string, before *time.Time, beforeID *string, limit int) ([]Play, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text, track_id::text, queue_id::text, sequence, previous_play_id::text,
		started_at, listened_ms, duration_ms, start_reason, outcome FROM listening_history
		WHERE user_id=$1 AND ($2::timestamptz IS NULL OR (started_at,id)<($2,$3::uuid))
		ORDER BY started_at DESC,id DESC LIMIT $4`, user, before, beforeID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Play, error) {
		var p Play
		err := row.Scan(&p.ID, &p.TrackID, &p.QueueID, &p.Sequence, &p.PreviousPlayID, &p.StartedAt, &p.ListenedMS, &p.DurationMS, &p.StartReason, &p.Outcome)
		p.StartedAt = p.StartedAt.UTC()
		return p, err
	})
}

func (s *Store) ListeningStats(ctx context.Context, user, id string) (ListeningStats, error) {
	var stats ListeningStats
	var total string
	err := s.pool.QueryRow(ctx, `SELECT t.id::text,count(h.id),count(h.id) FILTER (WHERE h.outcome='completed'),
		coalesce(sum(h.listened_ms),0)::text FROM tracks t
		LEFT JOIN listening_history h ON h.user_id=t.user_id AND h.track_id=t.id
		WHERE t.user_id=$1 AND t.id=$2 GROUP BY t.id`, user, id).Scan(&stats.TrackID, &stats.Starts, &stats.Completions, &total)
	stats.ListenedMS = json.Number(total)
	return stats, err
}
