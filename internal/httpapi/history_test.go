package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"testing"

	"audioservice/internal/store"
)

func testListeningHistory(t *testing.T, track string, request func(string, string, io.Reader, map[string]string) result,
	check func(result, int) []byte, owner, other map[string]string) {
	t.Helper()
	const first = "00000000-0000-4000-8000-000000000001"
	const second = "00000000-0000-4000-8000-000000000002"
	const queue = "10000000-0000-4000-8000-000000000001"
	event := map[string]any{"track_id": track, "queue_id": queue, "sequence": 1, "started_at": "2026-11-25T01:00:00.456Z",
		"listened_ms": 0, "duration_ms": 60000, "start_reason": "selection", "outcome": "in_progress"}
	put := func(id string, auth map[string]string, code int) {
		t.Helper()
		b, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		check(request("PUT", "/history/"+id, bytes.NewReader(b), auth), code)
	}
	statsPath := "/tracks/" + track + "/listening-stats"
	getStats := func() store.ListeningStats {
		return readJSON[store.ListeningStats](t, check(request("GET", statsPath, nil, owner), 200))
	}
	if stats := getStats(); stats.Starts != 0 || stats.TrackID != track {
		t.Fatalf("initial stats: %+v", stats)
	}
	put(first, other, 404)
	put(first, owner, 204)
	put(second, owner, 409)
	event["listened_ms"], event["outcome"] = 12000, "skipped"
	put(first, owner, 204)
	event["listened_ms"], event["outcome"] = 100, "in_progress"
	put(first, owner, 204)
	event["sequence"] = 2
	put(first, owner, 409)
	event["previous_play_id"], event["start_reason"] = first, "skip"
	event["listened_ms"], event["outcome"] = 60000, "completed"
	put(second, owner, 204)
	put(second, owner, 204)
	if got := getStats(); got.Starts != 2 || got.Completions != 1 || got.ListenedMS.String() != "72000" {
		t.Fatalf("final stats: %+v", got)
	}
	check(request("GET", statsPath, nil, other), 404)
	check(request("GET", "/history", nil, nil), 401)
	type page struct {
		Items []store.Play `json:"items"`
		Next  *string      `json:"next_cursor"`
	}
	read := func(query string, auth map[string]string) page {
		return readJSON[page](t, check(request("GET", "/history"+query, nil, auth), 200))
	}
	if foreign := read("", other); foreign.Items == nil || len(foreign.Items) != 0 {
		t.Fatalf("foreign history: %+v", foreign)
	}
	a := read("?limit=1", owner)
	if len(a.Items) != 1 || a.Items[0].ID != second || a.Next == nil || a.Items[0].PreviousPlayID == nil || *a.Items[0].PreviousPlayID != first {
		t.Fatalf("page 1: %+v", a)
	}
	b := read("?limit=1&cursor="+url.QueryEscape(*a.Next), owner)
	if len(b.Items) != 1 || b.Items[0].ID != first || b.Next != nil || b.Items[0].Outcome != "skipped" {
		t.Fatalf("page 2: %+v", b)
	}
}
