package wave

import (
	"encoding/binary"
	"math"
	"strconv"
	"testing"
)

func TestDailyContextAndExclusions(t *testing.T) {
	vector := func(x, y float32) []byte {
		v := make([]byte, 5120)
		binary.LittleEndian.PutUint32(v, math.Float32bits(x))
		binary.LittleEndian.PutUint32(v[4:], math.Float32bits(y))
		return v
	}
	catalog := []Candidate{{"anchor", vector(1, 0)}, {"a", vector(.8, .6)}, {"b", vector(.8, -.6)}, {"morning", vector(0, 1)}}
	excluded := map[string]bool{"morning": true}
	for _, tc := range []struct {
		weight float64
		want   string
	}{{2, "a"}, {-1, "b"}} {
		if got := Choose(catalog, "anchor", excluded, []Signal{{"morning", tc.weight}}); got != tc.want {
			t.Fatalf("got %q want %q", got, tc.want)
		}
	}
	for range 2 {
		id := Choose(catalog, "anchor", excluded, nil)
		if id == "" || excluded[id] {
			t.Fatal("repeated or missing candidate")
		}
		excluded[id] = true
	}
	if id := Choose(catalog, "anchor", excluded, nil); id != "" {
		t.Fatal("exhausted", id)
	}
}

func BenchmarkDailyRanking(b *testing.B) {
	for _, size := range []int{1000, 10000} {
		b.Run(strconv.Itoa(size), func(b *testing.B) {
			catalog := make([]Candidate, size)
			for i := range catalog {
				catalog[i] = Candidate{strconv.Itoa(i), make([]byte, Dimensions*4)}
			}
			var history []Signal
			for i := 0; i < 100; i++ {
				history = append(history, Signal{catalog[i].ID, 1})
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				Choose(catalog, "0", nil, history)
			}
		})
	}
}
