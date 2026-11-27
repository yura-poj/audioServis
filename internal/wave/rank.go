package wave

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
	"sort"
)

const Dimensions = 1280

type Candidate struct {
	ID     string
	Vector []byte
}
type Signal struct {
	ID     string
	Weight float64
}
type scored struct {
	id         string
	sound, day float64
	tie        uint64
}

func component(v []byte, i int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(v[i*4:])))
}

// Choose first narrows by sound, then ranks that neighbourhood by today's listening.
func Choose(catalog []Candidate, current string, excluded map[string]bool, history []Signal) string {
	weights := make(map[string]float64)
	var total float64
	for _, h := range history {
		weights[h.ID] += h.Weight
		total += math.Abs(h.Weight)
	}
	var profile [Dimensions]float64
	var anchor []byte
	for _, c := range catalog {
		if c.ID == current {
			anchor = c.Vector
		}
		if w := weights[c.ID]; w != 0 {
			for i := range profile {
				profile[i] += w * component(c.Vector, i) / total
			}
		}
	}
	var best []scored
	for _, c := range catalog {
		if c.ID == current || excluded[c.ID] {
			continue
		}
		item := scored{id: c.ID, tie: rand.Uint64()}
		for i := range profile {
			x := component(c.Vector, i)
			item.day += profile[i] * x
			if anchor != nil {
				item.sound += component(anchor, i) * x
			}
		}
		best = append(best, item)
	}
	if len(best) == 0 {
		return ""
	}
	if anchor != nil && len(best) > 5 {
		sort.Slice(best, func(i, j int) bool {
			if best[i].sound == best[j].sound {
				return best[i].tie > best[j].tie
			}
			return best[i].sound > best[j].sound
		})
		best = best[:5]
	}
	selected := best[0]
	for _, c := range best[1:] {
		delta := 0.7*(c.sound-selected.sound) + 0.3*(c.day-selected.day)
		if delta > 0 || delta == 0 && c.tie > selected.tie {
			selected = c
		}
	}
	return selected.id
}
