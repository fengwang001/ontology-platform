package sampler_test

import (
	"sync"
	"testing"

	"ontology/attrib"
	"ontology/sampler"
	"ontology/tree"
)

func TestSampling(t *testing.T) {
	cases := []struct {
		name        string
		ticks       int
		clock       func(i int) int64
		src         func(i int) []string
		busy        func(i int) bool
		wantSamples int64
		wantDropped int64
		wantInvalid int64
		wantAnomaly int64
	}{
		{
			name:        "137 drops in 1000 ticks",
			ticks:       1000,
			clock:       func(i int) int64 { return int64(i) * 10 },
			src:         func(i int) []string { return []string{"main", "work"} },
			busy:        func(i int) bool { return i < 137 },
			wantSamples: 863, wantDropped: 137,
		},
		{
			name:        "clock rollback skipped",
			ticks:       6,
			clock:       func(i int) int64 { return []int64{10, 20, 30, 5, 40, 50}[i] },
			src:         func(i int) []string { return []string{"main"} },
			wantSamples: 5, wantAnomaly: 1,
		},
		{
			name:  "empty stacks invalid",
			ticks: 10,
			clock: func(i int) int64 { return int64(i) },
			src: func(i int) []string {
				if i%3 == 0 {
					return nil
				}
				return []string{"a"}
			},
			wantSamples: 6, wantInvalid: 4,
		},
		{
			name:  "mixed drops invalid and rollback",
			ticks: 8,
			clock: func(i int) int64 { return []int64{1, 2, 3, 0, 4, 5, 6, 7}[i] },
			src: func(i int) []string {
				if i == 1 {
					return nil
				}
				return []string{"a", "b"}
			},
			busy:        func(i int) bool { return i == 2 },
			wantSamples: 5, wantDropped: 1, wantInvalid: 1, wantAnomaly: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New()
			i := 0
			sp := sampler.New(tr, 8,
				func() int64 { return tc.clock(i) },
				func() []string { return tc.src(i) },
				func() bool {
					if tc.busy == nil {
						return false
					}
					return tc.busy(i)
				})
			for j := 0; j < tc.ticks; j++ {
				i = j
				sp.Step()
			}
			selfSum := tree.SumSelf(tr.Root)
			if selfSum != tc.wantSamples || tr.Samples != tc.wantSamples {
				t.Fatalf("samples=%d self=%d want %d", tr.Samples, selfSum, tc.wantSamples)
			}
			if sp.Dropped() != tc.wantDropped || sp.Invalid() != tc.wantInvalid || sp.Anomalous() != tc.wantAnomaly {
				t.Fatalf("dropped=%d invalid=%d anomalous=%d", sp.Dropped(), sp.Invalid(), sp.Anomalous())
			}
			total := selfSum + sp.Dropped() + sp.Invalid() + sp.Anomalous()
			if total != int64(tc.ticks) || sp.Ticks() != int64(tc.ticks) {
				t.Fatalf("identity broken: %d != %d ticks", total, tc.ticks)
			}
		})
	}
}

func TestStopIdempotent(t *testing.T) {
	tr := tree.New()
	sp := sampler.New(tr, 8, func() int64 { return 0 }, func() []string { return []string{"a"} }, nil)
	sp.Stop()
	sp.Stop()
	sp.Stop()
	if !sp.Stopped() {
		t.Fatal("not stopped")
	}
	sp.Step()
	if sp.Ticks() != 0 || tr.Samples != 0 {
		t.Fatal("step after stop must be a no-op")
	}
}

func TestConcurrentQuery(t *testing.T) {
	tr := tree.New()
	clock := int64(0)
	sp := sampler.New(tr, 8,
		func() int64 { clock++; return clock },
		func() []string { return []string{"main", "f", "g"} },
		nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 20000; i++ {
			sp.Step()
		}
	}()
	for q := 0; q < 4; q++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				snap := tr.Snapshot()
				if tree.SumSelf(snap) != snap.Total {
					t.Error("observed half-updated tree")
					return
				}
				_ = attrib.BySelf(tr)
				_ = attrib.ByTotal(tr)
				_ = attrib.Excluding(tr, "f")
			}
		}()
	}
	wg.Wait()
	sp.Stop()
	sp.Stop()
	if sp.Ticks() != tr.Samples {
		t.Fatalf("ticks=%d samples=%d", sp.Ticks(), tr.Samples)
	}
}
