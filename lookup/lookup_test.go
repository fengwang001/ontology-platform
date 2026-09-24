package lookup

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sync"
	"testing"

	"ontology/dict"
)

func buildDict(t *testing.T, n, blockSize, k int) (*dict.Dict, []string) {
	t.Helper()
	entries := make([]string, n)
	for i := range entries {
		entries[i] = fmt.Sprintf("key-%07d", i)
	}
	d, err := dict.Build(entries, blockSize, k)
	if err != nil {
		t.Fatal(err)
	}
	return d, entries
}

func ceilLog2(n int) int {
	c := 0
	for p := 1; p < n; p <<= 1 {
		c++
	}
	return c
}

func TestFindPositions(t *testing.T) {
	d, _ := buildDict(t, 32, 8, 4)
	cases := []struct {
		name   string
		target string
		idx    int
		found  bool
	}{
		{"at-restart", "key-0000004", 4, true},
		{"between-restarts", "key-0000005", 5, true},
		{"between-missing", "key-0000005~", 6, false},
		{"below-block-head", "key-0000007~", 8, false},
		{"below-first", "aaa", 0, false},
		{"above-block-tail", "zzz", 32, false},
	}
	for _, tc := range cases {
		idx, found := Find(d, tc.target)
		if idx != tc.idx || found != tc.found {
			t.Errorf("%s: Find(%q) = %d,%v want %d,%v", tc.name, tc.target, idx, found, tc.idx, tc.found)
		}
	}
}

func TestFindMatchesLinear(t *testing.T) {
	d, entries := buildDict(t, 1000, 64, 16)
	targets := slices.Clone(entries)
	targets = append(targets, "key-0000500~", "!", "zzz", "key-0000999")
	for _, s := range targets {
		wantIdx, wantFound := slices.BinarySearch(entries, s)
		idx, found := Find(d, s)
		if idx != wantIdx || found != wantFound {
			t.Fatalf("Find(%q) = %d,%v want %d,%v", s, idx, found, wantIdx, wantFound)
		}
	}
}

func TestGetDecodeBound(t *testing.T) {
	d, entries := buildDict(t, 100000, 4096, 16)
	rng := rand.New(rand.NewPCG(1, 2))
	for range 500 {
		i := rng.IntN(len(entries))
		ResetCounters()
		s, err := Get(d, i)
		if err != nil || s != entries[i] {
			t.Fatalf("Get(%d) = %q,%v want %q", i, s, err, entries[i])
		}
		if decodes, _ := Counters(); decodes > 16 {
			t.Fatalf("Get(%d) decoded %d > K=16", i, decodes)
		}
	}
}

func TestFindCompareBound(t *testing.T) {
	d, entries := buildDict(t, 100000, 4096, 16)
	nR := (4096 + 15) / 16
	bound := int64(4*ceilLog2(nR) + 16)
	rng := rand.New(rand.NewPCG(3, 4))
	for range 500 {
		target := entries[rng.IntN(len(entries))]
		if rng.IntN(2) == 0 {
			target += "~"
		}
		ResetCounters()
		Find(d, target)
		_, compares := Counters()
		if compares > bound {
			t.Fatalf("Find(%q) compares %d > bound %d", target, compares, bound)
		}
	}
}

func TestKTradeoff(t *testing.T) {
	entries := make([]string, 10000)
	for i := range entries {
		entries[i] = fmt.Sprintf("user:%08d", i)
	}
	rng := rand.New(rand.NewPCG(5, 6))
	for _, k := range []int{1, 4, 16, 64} {
		d, err := dict.Build(entries, 128, k)
		if err != nil {
			t.Fatal(err)
		}
		var decodes, compares int64
		for range 200 {
			ResetCounters()
			Get(d, rng.IntN(len(entries)))
			dn, _ := Counters()
			decodes += dn
			ResetCounters()
			Find(d, entries[rng.IntN(len(entries))])
			_, cn := Counters()
			compares += cn
		}
		if avg := float64(decodes) / 200; avg > float64(k) {
			t.Fatalf("K=%d avg decodes %.1f > K", k, avg)
		}
		t.Logf("K=%d bytes=%d avgGetDecodes=%.1f avgFindCompares=%.1f",
			k, len(d.Serialize()), float64(decodes)/200, float64(compares)/200)
	}
}

func TestConcurrentReadWhileBuild(t *testing.T) {
	d, entries := buildDict(t, 10000, 256, 16)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(g), 9))
			for {
				select {
				case <-stop:
					return
				default:
				}
				i := rng.IntN(len(entries))
				if s, _ := Get(d, i); s != entries[i] {
					t.Errorf("Get(%d) = %q want %q", i, s, entries[i])
				}
				if idx, found := Find(d, entries[i]); !found || idx != i {
					t.Errorf("Find(%q) = %d,%v", entries[i], idx, found)
				}
			}
		}()
	}
	for range 50 {
		if _, err := dict.Build(entries, 256, 16); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}
