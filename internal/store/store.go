package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = pgx.ErrNoRows

type User struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

type Track struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	SizeBytes int64     `json:"size_bytes"`
	CreatedAt time.Time `json:"created_at"`
	Liked     bool      `json:"liked"`
}

type Playlist struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

const trackColumns = `id::text, title, size_bytes, created_at, liked`
const playlistColumns = `id::text, name, created_at`

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	if databaseURL == "" {
		return nil, errors.New("AUDIOSERVICE_DATABASE_URL is required")
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("invalid AUDIOSERVICE_DATABASE_URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() {
	s.pool.Close()
}

func CleanName(value string) (string, bool) {
	value = strings.TrimSpace(value)
	length := utf8.RuneCountInString(value)
	return value, utf8.ValidString(value) && length >= 1 && length <= 200 && !strings.ContainsRune(value, 0)
}

func trackFromRow(row pgx.CollectableRow) (Track, error) {
	track, err := pgx.RowToStructByPos[Track](row)
	track.CreatedAt = track.CreatedAt.UTC()
	return track, err
}

func playlistFromRow(row pgx.CollectableRow) (Playlist, error) {
	playlist, err := pgx.RowToStructByPos[Playlist](row)
	playlist.CreatedAt = playlist.CreatedAt.UTC()
	return playlist, err
}

func (s *Store) CreateUser(ctx context.Context, name string) (string, error) {
	name, valid := CleanName(name)
	if !valid {
		return "", errors.New("name must contain 1 to 200 characters")
	}
	var randomKey [32]byte
	rand.Read(randomKey[:])
	key := base64.RawURLEncoding.EncodeToString(randomKey[:])
	hash := sha256.Sum256([]byte(key))
	_, err := s.pool.Exec(ctx, `INSERT INTO users (display_name, key_hash) VALUES ($1, $2)`, name, hash[:])
	if err != nil {
		return "", err
	}
	return key, nil
}

func (s *Store) FindUser(ctx context.Context, key string) (User, error) {
	if len(key) != 43 {
		return User{}, ErrNotFound
	}
	hash := sha256.Sum256([]byte(key))
	var user User
	err := s.pool.QueryRow(ctx, `SELECT id::text, display_name FROM users WHERE key_hash = $1`, hash[:]).
		Scan(&user.ID, &user.DisplayName)
	return user, err
}

func (s *Store) ListPlaylists(ctx context.Context, userID string) ([]Playlist, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+playlistColumns+` FROM playlists WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, playlistFromRow)
}

func (s *Store) CreatePlaylist(ctx context.Context, userID, name string) (Playlist, error) {
	rows, err := s.pool.Query(ctx, `INSERT INTO playlists (user_id, name) VALUES ($1, $2) RETURNING `+playlistColumns, userID, name)
	if err != nil {
		return Playlist{}, err
	}
	return pgx.CollectExactlyOneRow(rows, playlistFromRow)
}

func (s *Store) RenamePlaylist(ctx context.Context, userID, id, name string) (Playlist, error) {
	rows, err := s.pool.Query(ctx, `UPDATE playlists SET name = $3 WHERE user_id = $1 AND id = $2 RETURNING `+playlistColumns, userID, id, name)
	if err != nil {
		return Playlist{}, err
	}
	return pgx.CollectExactlyOneRow(rows, playlistFromRow)
}

func (s *Store) DeletePlaylist(ctx context.Context, userID, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM playlists WHERE user_id = $1 AND id = $2`, userID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

func (s *Store) ListPlaylistTracks(ctx context.Context, userID, playlistID string) ([]Track, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT true FROM playlists WHERE user_id = $1 AND id = $2`, userID, playlistID).Scan(&exists)
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id::text, t.title, t.size_bytes, t.created_at, t.liked
		FROM playlist_tracks pt
		JOIN tracks t ON t.user_id = pt.user_id AND t.id = pt.track_id
		WHERE pt.user_id = $1 AND pt.playlist_id = $2
		ORDER BY pt.position
	`, userID, playlistID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, trackFromRow)
}

func lockPlaylist(ctx context.Context, tx pgx.Tx, userID, playlistID string) error {
	var id pgtype.UUID
	return tx.QueryRow(ctx, `SELECT id FROM playlists WHERE user_id = $1 AND id = $2 FOR UPDATE`, userID, playlistID).Scan(&id)
}

func addToLockedPlaylist(ctx context.Context, tx pgx.Tx, userID, playlistID, trackID string) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO playlist_tracks (user_id, playlist_id, track_id, position)
		SELECT $1::uuid, $2::uuid, $3::uuid, COALESCE(MAX(position), 0) + 1
		FROM playlist_tracks WHERE playlist_id = $2
		ON CONFLICT (playlist_id, track_id) DO NOTHING
	`, userID, playlistID, trackID)
	return err
}

func (s *Store) ChangeMembership(ctx context.Context, userID, playlistID, trackID string, add bool) error {
	return pgx.BeginTxFunc(ctx, s.pool, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		if err := lockPlaylist(ctx, tx, userID, playlistID); err != nil {
			return err
		}
		var id pgtype.UUID
		err := tx.QueryRow(ctx, `SELECT id FROM tracks WHERE user_id = $1 AND id = $2 FOR KEY SHARE`, userID, trackID).Scan(&id)
		if err != nil {
			return err
		}
		if add {
			return addToLockedPlaylist(ctx, tx, userID, playlistID, trackID)
		}
		_, err = tx.Exec(ctx, `DELETE FROM playlist_tracks WHERE user_id = $1 AND playlist_id = $2 AND track_id = $3`, userID, playlistID, trackID)
		return err
	})
}

func (s *Store) ListTracks(ctx context.Context, userID string) ([]Track, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+trackColumns+` FROM tracks WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, trackFromRow)
}

func (s *Store) Track(ctx context.Context, userID, id string) (Track, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+trackColumns+` FROM tracks WHERE user_id = $1 AND id = $2`, userID, id)
	if err != nil {
		return Track{}, err
	}
	return pgx.CollectExactlyOneRow(rows, trackFromRow)
}

// CreateTrack publishes the file before committing metadata. When the outcome of
// COMMIT is unknown, keepFile protects a possibly committed track's audio.
func (s *Store) CreateTrack(ctx context.Context, userID, playlistID, title string, size int64, publish func(Track) error) (track Track, keepFile bool, err error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Track{}, false, err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	if playlistID != "" {
		if err := lockPlaylist(ctx, tx, userID, playlistID); err != nil {
			return Track{}, false, err
		}
	}
	rows, err := tx.Query(ctx, `INSERT INTO tracks (user_id, title, size_bytes) VALUES ($1, $2, $3) RETURNING `+trackColumns, userID, title, size)
	if err != nil {
		return Track{}, false, err
	}
	track, err = pgx.CollectExactlyOneRow(rows, trackFromRow)
	if err != nil {
		return Track{}, false, err
	}
	if playlistID != "" {
		if err := addToLockedPlaylist(ctx, tx, userID, playlistID, track.ID); err != nil {
			return Track{}, false, err
		}
	}
	if _, err := tx.Exec(ctx, "INSERT INTO track_embeddings(track_id) VALUES ($1)", track.ID); err != nil {
		return Track{}, false, err
	}
	if err := publish(track); err != nil {
		return Track{}, false, err
	}
	err = tx.Commit(ctx)
	return track, !errors.Is(err, pgx.ErrTxCommitRollback), err
}

func (s *Store) RenameTrack(ctx context.Context, userID, id, title string) (Track, error) {
	rows, err := s.pool.Query(ctx, `UPDATE tracks SET title = $3 WHERE user_id = $1 AND id = $2 RETURNING `+trackColumns, userID, id, title)
	if err != nil {
		return Track{}, err
	}
	return pgx.CollectExactlyOneRow(rows, trackFromRow)
}

func (s *Store) SetLiked(ctx context.Context, userID, id string, liked bool) (Track, error) {
	rows, err := s.pool.Query(ctx, `UPDATE tracks SET liked = $3 WHERE user_id = $1 AND id = $2 RETURNING `+trackColumns, userID, id, liked)
	if err != nil {
		return Track{}, err
	}
	return pgx.CollectExactlyOneRow(rows, trackFromRow)
}

func (s *Store) DeleteTrack(ctx context.Context, userID, id string) (string, error) {
	var deletedID string
	err := s.pool.QueryRow(ctx, `DELETE FROM tracks WHERE user_id = $1 AND id = $2 RETURNING id::text`, userID, id).Scan(&deletedID)
	return deletedID, err
}
