package sample_test

import (
	"math/rand"
	"sort"
	"testing"

	"ontology/record"
	"ontology/sample"
)

const (
	chains   = 50
	perChain = 20
)

type item struct {
	trace string
	idx   int
	level record.Level
}

func buildItems() []item {
	var items []item
	for c := 0; c < chains; c++ {
		id := "trace-" + pad(c)
		for i := 0; i < perChain; i++ {
			items = append(items, item{id, i, record.Info})
		}
	}
	rng := rand.New(rand.NewSource(42))
	rng.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })
	return items
}

func pad(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}

func TestConsistentSampling(t *testing.T) {
	rates := []float64{0, 0.5, 1}
	for _, rate := range rates {
		s, err := sample.New(rate)
		if err != nil {
			t.Fatalf("rate %v: %v", rate, err)
		}
		items := buildItems()
		chainKeep := map[string]bool{}
		kept := 0
		for _, it := range items {
			r, _ := record.New(it.level, it.trace, map[string]any{"i": it.idx})
			ok := s.Decide(r)
			if prev, seen := chainKeep[it.trace]; seen && prev != ok {
				t.Fatalf("rate %v chain %s split decision", rate, it.trace)
			}
			chainKeep[it.trace] = ok
			if ok {
				kept++
			}
			if r.Incomplete {
				t.Fatalf("non-error record must never be incomplete")
			}
		}
		kc := 0
		for _, k := range chainKeep {
			if k {
				kc++
			}
		}
		t.Logf("rate=%.1f kept_records=%d/%d kept_chains=%d/%d hash=%d",
			rate, kept, len(items), kc, chains, s.HashCount())
		if rate == 0 && kept != 0 {
			t.Fatalf("rate 0 must drop all")
		}
		if rate == 1 && kept != len(items) {
			t.Fatalf("rate 1 must keep all")
		}
		if s.HashCount() != uint64(len(items)) {
			t.Fatalf("each decision must hash exactly once: %d", s.HashCount())
		}
		// 同一批追踪 ID 重复 20 次，决策完全相同。
		for n := 0; n < 20; n++ {
			for it := range chainKeep {
				if s.KeepTrace(it) != chainKeep[it] {
					t.Fatalf("decision for %s changed on repeat %d", it, n)
				}
			}
		}
	}
}

func TestErrorForceAndIncomplete(t *testing.T) {
	s, _ := sample.New(0.5)
	items := buildItems()
	dropped, keptChain := "", ""
	seen := map[string]bool{}
	for _, it := range items {
		if seen[it.trace] {
			continue
		}
		seen[it.trace] = true
		if s.KeepTrace(it.trace) && keptChain == "" {
			keptChain = it.trace
		}
		if !s.KeepTrace(it.trace) && dropped == "" {
			dropped = it.trace
		}
	}
	cases := []struct {
		name        string
		trace       string
		level       record.Level
		wantKeep    bool
		wantIncompl bool
	}{
		{"error on dropped chain marked incomplete", dropped, record.Error, true, true},
		{"fatal on dropped chain marked incomplete", dropped, record.Fatal, true, true},
		{"error on kept chain complete", keptChain, record.Error, true, false},
		{"info on dropped chain dropped", dropped, record.Info, false, false},
		{"empty trace error still force kept", "", record.Error, true, true},
	}
	for _, tc := range cases {
		r, _ := record.New(tc.level, tc.trace, nil)
		if got := s.Decide(r); got != tc.wantKeep ||
			r.Incomplete != tc.wantIncompl {
			t.Fatalf("%s: keep=%v incomplete=%v want %v/%v",
				tc.name, got, r.Incomplete, tc.wantKeep, tc.wantIncompl)
		}
	}
}

func TestConcurrentEqualsSerial(t *testing.T) {
	s, _ := sample.New(0.5)
	items := buildItems()
	run := func(parallel bool) []string {
		out := make([]string, len(items))
		if !parallel {
			for i, it := range items {
				r, _ := record.New(it.level, it.trace, nil)
				if s.Decide(r) {
					out[i] = it.trace + ":" + boolStr(r.Incomplete)
				}
			}
			return out
		}
		done := make(chan struct{})
		workers := 8
		for w := 0; w < workers; w++ {
			go func(w int) {
				for i := w; i < len(items); i += workers {
					it := items[i]
					r, _ := record.New(it.level, it.trace, nil)
					if s.Decide(r) {
						out[i] = it.trace + ":" + boolStr(r.Incomplete)
					}
				}
				done <- struct{}{}
			}(w)
		}
		for w := 0; w < workers; w++ {
			<-done
		}
		return out
	}
	serial := run(false)
	concurrent := run(true)
	sort.Strings(serial)
	sort.Strings(concurrent)
	if len(serial) != len(concurrent) {
		t.Fatalf("kept set size differs: %d vs %d", len(serial), len(concurrent))
	}
	for i := range serial {
		if serial[i] != concurrent[i] {
			t.Fatalf("element %d differs: %q vs %q", i, serial[i], concurrent[i])
		}
	}
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
