package chat

import (
	"math/rand"
	"sort"
	"testing"
)

// TestSeqsetAgainstSlice cross-checks the treap against a plain sorted slice
// under random insert/remove sequences.
func TestSeqsetAgainstSlice(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		set := &seqset{}
		var model []int
		for i := 0; i < 300; i++ {
			key := rng.Intn(500)
			idx := sort.SearchInts(model, key)
			present := idx < len(model) && model[idx] == key
			if rng.Intn(2) == 0 {
				if !present {
					set.insert(key)
					model = append(model, 0)
					copy(model[idx+1:], model[idx:])
					model[idx] = key
				}
			} else if present {
				set.remove(key)
				model = append(model[:idx], model[idx+1:]...)
			}
			if set.len() != len(model) {
				t.Fatalf("trial %d step %d: len=%d want %d", trial, i, set.len(), len(model))
			}
			probe := rng.Intn(500)
			want := len(model) - sort.SearchInts(model, probe+1)
			if got := set.countGreater(probe); got != want {
				t.Fatalf("trial %d step %d: countGreater(%d)=%d want %d", trial, i, probe, got, want)
			}
		}
	}
}
