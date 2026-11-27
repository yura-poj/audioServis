package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sync"
	"testing"

	"audioservice/internal/httpapi"
	"audioservice/internal/media"
	"audioservice/internal/store"
)

type result struct {
	status int
	header http.Header
	body   []byte
	err    error
}

func TestServerRoutesInitialize(t *testing.T) {
	handler := httpapi.New(nil, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/wave/next", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "POST" {
		t.Fatalf("wave route: status %d, Allow %q", response.Code, response.Header().Get("Allow"))
	}
}

func TestPersonalLibrary(t *testing.T) {
	dsn := os.Getenv("AUDIOSERVICE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set AUDIOSERVICE_TEST_DATABASE_URL to a disposable PostgreSQL instance")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for range 2 {
		if err := db.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	keyA, err := db.CreateUser(ctx, "Test listener A")
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := db.CreateUser(ctx, "Test listener B")
	if err != nil {
		t.Fatal(err)
	}
	files, err := media.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.New(db, files))
	defer server.Close()

	request := func(method, path string, body io.Reader, headers map[string]string) result {
		req, err := http.NewRequest(method, server.URL+"/api/v1"+path, body)
		if err != nil {
			return result{err: err}
		}
		for name, value := range headers {
			req.Header.Set(name, value)
		}
		reply, err := server.Client().Do(req)
		if err != nil {
			return result{err: err}
		}
		defer reply.Body.Close()
		data, err := io.ReadAll(reply.Body)
		return result{status: reply.StatusCode, header: reply.Header, body: data, err: err}
	}
	check := func(reply result, code int) []byte {
		t.Helper()
		if reply.err != nil || reply.status != code {
			t.Fatalf("got status %d, want %d, body %q, error %v", reply.status, code, reply.body, reply.err)
		}
		return reply.body
	}
	authA := map[string]string{"Authorization": "Bearer " + keyA}
	authB := map[string]string{"Authorization": "Bearer " + keyB}
	mp3A := map[string]string{"Authorization": "Bearer " + keyA, "Content-Type": "audio/mpeg"}
	createPlaylist := func(name string, auth map[string]string) store.Playlist {
		t.Helper()
		input, _ := json.Marshal(map[string]string{"name": name})
		body := check(request("POST", "/playlists", bytes.NewReader(input), auth), 201)
		return readJSON[store.Playlist](t, body)
	}
	playlistA := createPlaylist("Плейлист A", authA)
	playlistB := createPlaylist("Плейлист B", authA)
	foreign := createPlaylist("Private playlist", authB)
	check(request("GET", "/playlists/"+foreign.ID+"/tracks", nil, authA), 404)
	check(request("GET", "/me", nil, nil), 401)

	payload, err := os.ReadFile("../../testdata/mp3/cbr.mp3")
	if err != nil {
		t.Fatal(err)
	}
	upload := func(title, playlistID string) store.Track {
		t.Helper()
		path := "/tracks?title=" + url.QueryEscape(title) + "&playlist_id=" + playlistID
		body := check(request("POST", path, bytes.NewReader(payload), mp3A), 201)
		return readJSON[store.Track](t, body)
	}
	trackA := upload("Песня + A", playlistA.ID)
	waveBody := func(id string) io.Reader {
		b, err := json.Marshal(map[string]any{"current_track_id": id, "excluded_track_ids": []string{id}})
		if err != nil {
			t.Fatal(err)
		}
		return bytes.NewReader(b)
	}
	check(request("POST", "/wave/next", waveBody(trackA.ID), authB), 404)
	check(request("POST", "/wave/next", waveBody(trackA.ID), authA), 409)
	if err := db.SaveEmbedding(ctx, trackA.ID, make([]byte, 5120)); err != nil {
		t.Fatal(err)
	}
	check(request("POST", "/wave/next", waveBody(trackA.ID), authA), 204)
	check(request("POST", "/wave/next", bytes.NewReader([]byte("{}")), authB), 204)
	waveFirst := readJSON[store.Track](t, check(request("POST", "/wave/next", bytes.NewReader([]byte("{}")), authA), 200))
	if waveFirst.ID != trackA.ID {
		t.Fatal("unexpected wave recommendation", waveFirst.ID)
	}
	if trackA.Title != "Песня + A" || trackA.SizeBytes != int64(len(payload)) {
		t.Fatalf("incorrect upload metadata: %+v", trackA)
	}
	trackB := upload("B", "")
	trackC := upload("C", "")
	check(request("POST", "/tracks?title=foreign&playlist_id="+foreign.ID, bytes.NewReader(payload), mp3A), 404)
	check(request("POST", "/tracks?title=empty", nil, mp3A), 400)

	pathFor := func(playlist, track string) string { return "/playlists/" + playlist + "/tracks/" + track }
	check(request("PUT", pathFor(playlistB.ID, trackA.ID), nil, authA), 204)
	check(request("PUT", pathFor(foreign.ID, trackA.ID), nil, authB), 404)

	var wg sync.WaitGroup
	replies := make(chan result, 3)
	for _, trackID := range []string{trackB.ID, trackC.ID, trackB.ID} {
		wg.Go(func() {
			replies <- request("PUT", pathFor(playlistA.ID, trackID), nil, authA)
		})
	}
	wg.Wait()
	close(replies)
	for reply := range replies {
		check(reply, 204)
	}
	listTracks := func(path string, auth map[string]string) []store.Track {
		t.Helper()
		return readJSON[struct {
			Items []store.Track `json:"items"`
		}](t, check(request("GET", path, nil, auth), 200)).Items
	}
	orderedPath := "/playlists/" + playlistA.ID + "/tracks"
	firstOrder := listTracks(orderedPath, authA)
	if len(firstOrder) != 3 || firstOrder[0].ID != trackA.ID {
		t.Fatalf("incorrect order or membership: %+v", firstOrder)
	}
	check(request("PUT", pathFor(playlistA.ID, trackA.ID), nil, authA), 204)
	if after := listTracks(orderedPath, authA); !reflect.DeepEqual(firstOrder, after) {
		t.Fatal("repeated PUT changed the playlist order")
	}
	if catalog := listTracks("/tracks", authA); len(catalog) != 3 {
		t.Fatalf("expected three unique tracks after repeated associations, got %d", len(catalog))
	}
	if got := listTracks("/tracks", authB); len(got) != 0 {
		t.Fatal("foreign tracks appeared in another catalog")
	}

	testListeningHistory(t, trackA.ID, request, check, authA, authB)
	mediaPath := "/media/" + trackA.ID
	cookie := map[string]string{"Cookie": "audioservice_media=" + keyA}
	check(request("GET", "/me", nil, cookie), 401)
	check(request("GET", mediaPath, nil, authB), 404)
	if got := check(request("GET", mediaPath, nil, cookie), 200); !bytes.Equal(got, payload) {
		t.Fatal("full media payload changed")
	}
	head := request("HEAD", mediaPath, nil, cookie)
	check(head, 200)
	if head.header.Get("Content-Length") != fmt.Sprint(len(payload)) || len(head.body) != 0 {
		t.Fatal("HEAD did not describe the same file")
	}
	rangeReq := map[string]string{"Cookie": cookie["Cookie"], "Range": "bytes=2-5"}
	partial := request("GET", mediaPath, nil, rangeReq)
	if got := check(partial, 206); !bytes.Equal(got, payload[2:6]) || partial.header.Get("Content-Range") != fmt.Sprintf("bytes 2-5/%d", len(payload)) {
		t.Fatal("incorrect partial media response")
	}

	liked := readJSON[store.Track](t, check(request("PUT", "/tracks/"+trackA.ID+"/like", nil, authA), 200))
	if !liked.Liked {
		t.Fatal("like did not persist")
	}
	check(request("DELETE", "/tracks/"+trackA.ID+"/like", nil, authB), 404)
	unliked := readJSON[store.Track](t, check(request("DELETE", "/tracks/"+trackA.ID+"/like", nil, authA), 200))
	if unliked.Liked {
		t.Fatal("unlike did not persist")
	}
	renamed := readJSON[store.Track](t, check(request("PATCH", "/tracks/"+trackA.ID, bytes.NewBufferString(`{"title":"Обновлённый трек"}`), authA), 200))
	if renamed.Title != "Обновлённый трек" {
		t.Fatal("track rename did not persist")
	}
	check(request("PATCH", "/playlists/"+playlistB.ID, bytes.NewBufferString(`{"name":"Другой список"}`), authA), 200)
	check(request("DELETE", pathFor(playlistA.ID, trackA.ID), nil, authA), 204)
	if got := listTracks("/playlists/"+playlistB.ID+"/tracks", authA); len(got) != 1 || got[0].ID != trackA.ID {
		t.Fatal("removing a membership changed another playlist")
	}
	check(request("DELETE", "/playlists/"+playlistB.ID, nil, authA), 204)
	check(request("GET", mediaPath, nil, authA), 200)
	check(request("DELETE", "/tracks/"+trackA.ID, nil, authA), 204)
	check(request("GET", mediaPath, nil, authA), 404)
	after := readJSON[struct {
		Items []store.Play `json:"items"`
	}](t, check(request("GET", "/history", nil, authA), 200))
	if len(after.Items) != 0 {
		t.Fatalf("deleted track history: %+v", after.Items)
	}

}

func readJSON[T any](t *testing.T, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("invalid JSON response %s: %v", data, err)
	}
	return value
}
