package httpapi

import (
	"net/http"

	"audioservice/internal/store"
)

func (s *server) listPlaylists(w http.ResponseWriter, r *http.Request, user store.User) {
	items, err := s.store.ListPlaylists(r.Context(), user.ID)
	if err != nil {
		respondError(w, err)
		return
	}
	writeList(w, items)
}

func (s *server) writePlaylist(w http.ResponseWriter, r *http.Request, user store.User) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	name, valid := requireName(w, input.Name)
	if !valid {
		return
	}
	var playlist store.Playlist
	var err error
	status := http.StatusOK
	if r.Method == http.MethodPost {
		playlist, err = s.store.CreatePlaylist(r.Context(), user.ID, name)
		status = http.StatusCreated
	} else {
		id := r.PathValue("id")
		if !requireID(w, id) {
			return
		}
		playlist, err = s.store.RenamePlaylist(r.Context(), user.ID, id, name)
	}
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, status, playlist)
}

func (s *server) deletePlaylist(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	if err := s.store.DeletePlaylist(r.Context(), user.ID, id); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) playlistTracks(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	items, err := s.store.ListPlaylistTracks(r.Context(), user.ID, id)
	if err != nil {
		respondError(w, err)
		return
	}
	writeList(w, items)
}

func (s *server) membership(w http.ResponseWriter, r *http.Request, user store.User) {
	playlistID, trackID := r.PathValue("id"), r.PathValue("track_id")
	if !requireID(w, playlistID) || !requireID(w, trackID) {
		return
	}
	if err := s.store.ChangeMembership(r.Context(), user.ID, playlistID, trackID, r.Method == http.MethodPut); err != nil {
		respondError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) like(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	track, err := s.store.SetLiked(r.Context(), user.ID, id, r.Method == http.MethodPut)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, track)
}
