package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"audioservice/internal/media"
	"audioservice/internal/store"

	"github.com/jackc/pgx/v5/pgtype"
)

type server struct {
	store *store.Store
	files *media.Files
	wake  chan<- struct{}
}

type action func(http.ResponseWriter, *http.Request, store.User)

func New(database *store.Store, files *media.Files) http.Handler {
	return NewWithAnalyzer(database, files, nil)
}

func NewWithAnalyzer(database *store.Store, files *media.Files, wake chan<- struct{}) http.Handler {
	s := &server{store: database, files: files, wake: wake}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/wave/next", s.authorize(false, s.nextWave))
	mux.HandleFunc("GET /api/v1/me", s.authorize(false, func(w http.ResponseWriter, _ *http.Request, user store.User) {
		writeJSON(w, http.StatusOK, user)
	}))
	mux.HandleFunc("GET /api/v1/playlists", s.authorize(false, s.listPlaylists))
	mux.HandleFunc("POST /api/v1/playlists", s.authorize(false, s.writePlaylist))
	mux.HandleFunc("PATCH /api/v1/playlists/{id}", s.authorize(false, s.writePlaylist))
	mux.HandleFunc("DELETE /api/v1/playlists/{id}", s.authorize(false, s.deletePlaylist))
	mux.HandleFunc("GET /api/v1/playlists/{id}/tracks", s.authorize(false, s.playlistTracks))
	mux.HandleFunc("PUT /api/v1/playlists/{id}/tracks/{track_id}", s.authorize(false, s.membership))
	mux.HandleFunc("DELETE /api/v1/playlists/{id}/tracks/{track_id}", s.authorize(false, s.membership))
	mux.HandleFunc("GET /api/v1/tracks", s.authorize(false, s.listTracks))
	mux.HandleFunc("POST /api/v1/tracks", s.authorize(false, s.upload))
	mux.HandleFunc("PATCH /api/v1/tracks/{id}", s.authorize(false, s.renameTrack))
	mux.HandleFunc("DELETE /api/v1/tracks/{id}", s.authorize(false, s.deleteTrack))
	mux.HandleFunc("PUT /api/v1/tracks/{id}/like", s.authorize(false, s.like))
	mux.HandleFunc("DELETE /api/v1/tracks/{id}/like", s.authorize(false, s.like))
	mux.HandleFunc("GET /api/v1/media/{id}", s.authorize(true, s.serveAudio))
	mux.HandleFunc("PUT /api/v1/history/{id}", s.authorize(false, s.recordPlay))
	mux.HandleFunc("GET /api/v1/history", s.authorize(false, s.history))
	mux.HandleFunc("GET /api/v1/tracks/{id}/listening-stats", s.authorize(false, s.listeningStats))
	return mux
}

func (s *server) authorize(allowMediaCookie bool, next action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		scheme, key, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") {
			key = ""
		}
		if scheme == "" && allowMediaCookie {
			if cookie, err := r.Cookie("audioservice_media"); err == nil {
				key = cookie.Value
			}
		}
		user, err := s.store.FindUser(r.Context(), key)
		if errors.Is(err, store.ErrNotFound) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			fail(w, http.StatusUnauthorized, "unauthorized", "A valid personal key is required")
			return
		}
		if err != nil {
			respondError(w, err)
			return
		}
		next(w, r, user)
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Debug("write JSON response", "error", err)
	}
}

func writeList[T any](w http.ResponseWriter, items []T) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, struct {
		Items []T `json:"items"`
	}{Items: items})
}

func fail(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func respondError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not_found", "Resource not found")
		return
	}
	slog.Error("request failed", "error", err)
	fail(w, http.StatusInternalServerError, "internal_error", "The server could not complete the request")
}

func decodeBody(w http.ResponseWriter, r *http.Request, value any) bool {
	return decodeLimitedBody(w, r, value, 16<<10)
}
func decodeLimitedBody(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err == nil {
		var extra any
		if errors.Is(decoder.Decode(&extra), io.EOF) {
			return true
		}
	}
	fail(w, http.StatusBadRequest, "invalid_json", "Expected one JSON object")
	return false
}

func validID(id string) bool {
	var uuid pgtype.UUID
	return uuid.Scan(id) == nil && uuid.Valid
}

func requireID(w http.ResponseWriter, id string) bool {
	if validID(id) {
		return true
	}
	respondError(w, store.ErrNotFound)
	return false
}

func requireName(w http.ResponseWriter, value string) (string, bool) {
	name, valid := store.CleanName(value)
	if !valid {
		fail(w, http.StatusBadRequest, "invalid_name", "Names must contain 1 to 200 characters")
	}
	return name, valid
}
