package httpapi

import (
	"encoding/binary"

	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
	"time"

	"audioservice/internal/media"
	"audioservice/internal/store"
	"audioservice/internal/wave"
)

// This opt-in measurement isolates file serving and ranking from database and device latency.
func TestWaveLatency(t *testing.T) {
	path := os.Getenv("WAVE_BENCH_MP3")
	if path == "" {
		t.Skip("set WAVE_BENCH_MP3 to a local MP3")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files, err := media.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	track := store.Track{ID: "benchmark", CreatedAt: time.Now()}
	if err = os.WriteFile(filepath.Join(dir, "audio", track.ID+".mp3"), data, 0600); err != nil {
		t.Fatal(err)
	}
	s := server{files: files}
	for _, n := range []int{1000, 10000} {
		catalog := make([]wave.Candidate, n)
		var daily []wave.Signal
		for i := range catalog {
			v := make([]byte, 5120)
			binary.LittleEndian.PutUint32(v[(i%1280)*4:], math.Float32bits(1))
			catalog[i] = wave.Candidate{ID: strconv.Itoa(i), Vector: v}
			if i < 100 {
				daily = append(daily, wave.Signal{ID: catalog[i].ID, Weight: 1})
			}
		}
		mux := http.NewServeMux()
		mux.HandleFunc("/media", func(w http.ResponseWriter, r *http.Request) { s.serveTrackFile(w, r, track) })
		mux.HandleFunc("/next", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, wave.Choose(catalog, "0", nil, daily)) })
		server := httptest.NewServer(mux)
		client := server.Client()
		get := func(path string) (float64, float64) {
			start := time.Now()
			resp, err := client.Get(server.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 {
				t.Fatal(resp.Status)
			}
			var b [1]byte
			if _, err = io.ReadFull(resp.Body, b[:]); err != nil {
				t.Fatal(err)
			}
			first := time.Since(start)
			copied, err := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if err != nil || path == "/media" && copied+1 != int64(len(data)) {
				t.Fatal("incomplete body", err)
			}
			return float64(first.Microseconds()) / 1000, float64(time.Since(start).Microseconds()) / 1000
		}
		stats := map[string][]float64{}
		add := func(k string, v float64) { stats[k] = append(stats[k], v) }
		for i := 0; i < 35; i++ {
			for j := 0; j < 2; j++ {
				with := (i+j)%2 == 0
				start := time.Now()
				if with {
					_, next := get("/next")
					if i >= 5 {
						add("choice", next)
					}
				}
				first, full := get("/media")
				if i >= 5 {
					prefix := "ordinary"
					if with {
						prefix = "wave"
						add("cold_first", float64(time.Since(start).Microseconds())/1000-full+first)
					}
					add(prefix+"_first", first)
					add(prefix+"_full", full)
				}
			}
		}
		for _, key := range []string{"ordinary_first", "wave_first", "ordinary_full", "wave_full", "choice", "cold_first"} {
			v := stats[key]
			sort.Float64s(v)
			t.Logf("%d %s p50 %.3f ms p95 %.3f ms", n, key, v[14], v[28])
		}
		server.Close()
	}
}
