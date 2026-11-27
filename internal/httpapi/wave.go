package httpapi

import (
	"errors"
	"net/http"
	"time"

	"audioservice/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *server) nextWave(w http.ResponseWriter, r *http.Request, user store.User) {
	var in struct {
		Current  *string  `json:"current_track_id"`
		Excluded []string `json:"excluded_track_ids"`
	}
	if !decodeLimitedBody(w, r, &in, 8<<20) {
		return
	}
	current := ""
	if in.Current != nil {
		var id pgtype.UUID
		if id.Scan(*in.Current) != nil || !id.Valid {
			fail(w, 400, "invalid_id", "Invalid current track ID")
			return
		}
		current = id.String()
	}
	excluded := map[string]bool{}
	for _, id := range in.Excluded {
		var uuid pgtype.UUID
		if uuid.Scan(id) != nil || !uuid.Valid {
			fail(w, 400, "invalid_id", "Invalid excluded ID")
			return
		}
		excluded[uuid.String()] = true
	}
	track, err := s.store.NextWave(r.Context(), user.ID, current, excluded, time.Now().Local())
	switch {
	case errors.Is(err, store.ErrWavePending):
		fail(w, 409, "wave_not_ready", "Треки ещё анализируются.")
	case errors.Is(err, store.ErrWaveFailed):
		fail(w, 422, "wave_analysis_failed", "Не удалось проанализировать трек.")
	case err != nil:
		respondError(w, err)
	case track.ID == "":
		w.WriteHeader(204)
	default:
		writeJSON(w, 200, track)
	}
}
