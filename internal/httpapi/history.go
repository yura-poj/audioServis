package httpapi

import (
	"encoding/base64"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"audioservice/internal/store"
)

func (s *server) recordPlay(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	var p store.PlaySnapshot
	// A zero-length first snapshot is valid, an absent duration counter is not.
	p.ListenedMS = -1
	if !decodeBody(w, r, &p) {
		return
	}
	if !validID(p.TrackID) || !validID(p.QueueID) || p.Sequence < 1 || p.StartedAt.IsZero() || p.ListenedMS < 0 ||
		(p.DurationMS != nil && *p.DurationMS <= 0) || (p.PreviousPlayID != nil && (!validID(*p.PreviousPlayID) || strings.EqualFold(*p.PreviousPlayID, id))) ||
		!slices.Contains([]string{"selection", "queue", "shuffle", "skip", "automatic"}, p.StartReason) ||
		!slices.Contains([]string{"in_progress", "completed", "skipped", "stopped", "error", "interrupted"}, p.Outcome) {
		fail(w, 400, "invalid_play", "Invalid listening history snapshot")
		return
	}
	err := s.store.RecordPlay(r.Context(), user.ID, id, p)
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, 404, "track_not_found", "Track not found")
	case errors.Is(err, store.ErrPlayConflict):
		fail(w, 409, "play_conflict", "Play ID or queue position conflicts with another playback")
	case err != nil:
		respondError(w, err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *server) history(w http.ResponseWriter, r *http.Request, user store.User) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			fail(w, 400, "invalid_limit", "Limit must be between 1 and 500")
			return
		}
		limit = n
	}
	var before *time.Time
	var beforeID *string
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		data, err := base64.RawURLEncoding.DecodeString(raw)
		ts, id, ok := strings.Cut(string(data), "|")
		stamp, timeErr := time.Parse(time.RFC3339Nano, ts)
		if err != nil || !ok || timeErr != nil || !validID(id) {
			fail(w, 400, "invalid_cursor", "Invalid history cursor")
			return
		}
		before, beforeID = &stamp, &id
	}
	items, err := s.store.History(r.Context(), user.ID, before, beforeID, limit+1)
	if err != nil {
		respondError(w, err)
		return
	}
	var next *string
	if len(items) > limit {
		items = items[:limit]
		last := items[limit-1]
		cursor := base64.RawURLEncoding.EncodeToString([]byte(last.StartedAt.Format(time.RFC3339Nano) + "|" + last.ID))
		next = &cursor
	}
	writeJSON(w, 200, struct {
		Items []store.Play `json:"items"`
		Next  *string      `json:"next_cursor"`
	}{items, next})
}

func (s *server) listeningStats(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	stats, err := s.store.ListeningStats(r.Context(), user.ID, id)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, 200, stats)
}
