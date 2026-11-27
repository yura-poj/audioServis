package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"

	"audioservice/internal/media"
	"audioservice/internal/store"
)

func (s *server) listTracks(w http.ResponseWriter, r *http.Request, user store.User) {
	items, err := s.store.ListTracks(r.Context(), user.ID)
	if err != nil {
		respondError(w, err)
		return
	}
	writeList(w, items)
}

func (s *server) upload(w http.ResponseWriter, r *http.Request, user store.User) {
	contentType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if contentType != "audio/mpeg" {
		fail(w, http.StatusUnsupportedMediaType, "unsupported_type", "Content-Type must be audio/mpeg")
		return
	}
	if r.ContentLength > media.MaxSize {
		fail(w, http.StatusRequestEntityTooLarge, "file_too_large", "Maximum MP3 size is 64 MiB")
		return
	}
	query := r.URL.Query()
	title, valid := requireName(w, query.Get("title"))
	if !valid {
		return
	}
	playlistID := query.Get("playlist_id")
	if playlistID != "" && !validID(playlistID) {
		fail(w, http.StatusBadRequest, "invalid_playlist_id", "Expected a playlist UUID")
		return
	}
	tempPath, size, err := s.files.Stage(http.MaxBytesReader(w, r.Body, media.MaxSize))
	if err != nil {
		var overLimit *http.MaxBytesError
		switch {
		case errors.As(err, &overLimit):
			fail(w, http.StatusRequestEntityTooLarge, "file_too_large", "Maximum MP3 size is 64 MiB")
		case errors.Is(err, io.ErrUnexpectedEOF):
			fail(w, http.StatusBadRequest, "incomplete_upload", "The request body ended early")
		default:
			respondError(w, err)
		}
		return
	}
	defer os.Remove(tempPath)
	if size == 0 {
		fail(w, http.StatusBadRequest, "empty_upload", "The MP3 body is empty")
		return
	}
	if err := media.ValidateMP3(tempPath, size); err != nil {
		if errors.Is(err, media.ErrInvalidMP3) {
			fail(w, http.StatusUnprocessableEntity, "invalid_mp3", "Формат MP3 не поддерживается или файл повреждён.")
		} else {
			respondError(w, err)
		}
		return
	}
	var publishedID string
	track, keepFile, err := s.store.CreateTrack(r.Context(), user.ID, playlistID, title, size, func(track store.Track) error {
		if err := s.files.Publish(tempPath, track.ID); err != nil {
			return err
		}
		publishedID = track.ID
		return nil
	})
	if err != nil {
		if publishedID != "" && !keepFile {
			s.deleteFile(publishedID)
		}
		respondError(w, err)
		return
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	writeJSON(w, http.StatusCreated, track)
}

func (s *server) renameTrack(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	var input struct {
		Title string `json:"title"`
	}
	if !decodeBody(w, r, &input) {
		return
	}
	title, valid := requireName(w, input.Title)
	if !valid {
		return
	}
	track, err := s.store.RenameTrack(r.Context(), user.ID, id, title)
	if err != nil {
		respondError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, track)
}

func (s *server) deleteTrack(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	deletedID, err := s.store.DeleteTrack(r.Context(), user.ID, id)
	if err != nil {
		respondError(w, err)
		return
	}
	s.deleteFile(deletedID)
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deleteFile(id string) {
	if err := s.files.Delete(id); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Error("remove unpublished audio file", "track", id, "error", err)
	}
}

func (s *server) serveAudio(w http.ResponseWriter, r *http.Request, user store.User) {
	id := r.PathValue("id")
	if !requireID(w, id) {
		return
	}
	track, err := s.store.Track(r.Context(), user.ID, id)
	if err != nil {
		respondError(w, err)
		return
	}
	s.serveTrackFile(w, r, track)
}

func (s *server) serveTrackFile(w http.ResponseWriter, r *http.Request, track store.Track) {
	file, err := s.files.Read(track.ID)
	if errors.Is(err, os.ErrNotExist) {
		respondError(w, store.ErrNotFound)
		return
	}
	if err != nil {
		respondError(w, err)
		return
	}
	defer file.Close()
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, track.ID+".mp3", track.CreatedAt, file)
}
