package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"audioservice/internal/media"
	"audioservice/internal/store"
)

func TestInvalidMP3RejectedBeforePublication(t *testing.T) {
	dir := t.TempDir()
	files, err := media.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := server{files: files}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tracks?title=test", strings.NewReader("not audio"))
	r.Header.Set("Content-Type", "audio/mpeg")
	w := httptest.NewRecorder()
	s.upload(w, r, store.User{})
	var body struct{ Error struct{ Code string } }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 422 || body.Error.Code != "invalid_mp3" {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	entries, err := os.ReadDir(filepath.Join(dir, "audio"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging directory: %v, %v", entries, err)
	}
}

func TestOversizeUploadWithoutContentLength(t *testing.T) {
	dir := t.TempDir()
	files, err := media.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := server{files: files}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tracks?title=large", io.LimitReader(zeroReader{}, media.MaxSize+1))
	r.Header.Set("Content-Type", "audio/mpeg")
	w := httptest.NewRecorder()
	s.upload(w, r, store.User{})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", w.Code)
	}
	entries, err := os.ReadDir(filepath.Join(dir, "audio"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging directory: %v, %v", entries, err)
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}
