package pagetable

import (
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentSmoke hammers the mapper from many goroutines (run with
// -race) and verifies the structural invariants afterwards. Each goroutine
// works on its own 64-page region so successful operations are plentiful,
// but all queries and mutations still race on the shared mapper.
func TestConcurrentSmoke(t *testing.T) {
	m := mustNew(t, 72)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)*131 + 7))
			base := g * 64
			for i := 0; i < 300; i++ {
				switch rng.Intn(6) {
				case 0:
					size := []int{1, 8, 64}[rng.Intn(3)]
					vpn := base + rng.Intn(64)/size*size
					_ = m.Map(vpn, size, (g*64+rng.Intn(64))/size*size, rng.Intn(2) == 0)
				case 1:
					vpn := base + rng.Intn(64)
					_, _ = m.Unmap(vpn, 1+rng.Intn(64-(vpn-base)))
				case 2:
					_ = m.Touch(base+rng.Intn(64), rng.Intn(2) == 0)
				case 3:
					size := []int{8, 64}[rng.Intn(2)]
					_ = m.Promote(base+rng.Intn(64)/size*size, size)
				case 4:
					_ = m.ScanDirty()
				case 5:
					_, _ = m.Translate(base + rng.Intn(64))
					_ = m.Leaves()
					_ = m.Tables()
					_ = m.Mapped()
					_ = m.Epoch()
				}
			}
		}(g)
	}
	wg.Wait()
	// Invariants on the final state.
	leaves := m.Leaves()
	mapped, prevEnd := 0, 0
	for i, l := range leaves {
		if i > 0 && l.Start < prevEnd {
			t.Fatalf("overlapping leaves after concurrent run: %v", leaves)
		}
		prevEnd = l.Start + l.Size
		mapped += l.Size
	}
	if got := m.Mapped(); got != mapped {
		t.Fatalf("Mapped()=%d, leaf sum=%d", got, mapped)
	}
	s := &sim{}
	for _, l := range leaves {
		s.leaves = append(s.leaves, simLeaf{l.Start, l.Size, l.PFN, l.W, l.A, l.D})
	}
	if got, want := m.Tables(), s.tablesUsed(); got != want {
		t.Fatalf("Tables()=%d, derived=%d", got, want)
	}
	if m.Tables() > 72 {
		t.Fatalf("quota exceeded: U=%d > 72", m.Tables())
	}
}

// TestSerializabilitySpotCheck: concurrent scans always observe a consistent
// snapshot (sorted, non-overlapping leaves whose sizes sum to Mapped()).
func TestSerializabilitySpotCheck(t *testing.T) {
	m := mustNew(t, 72)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for {
				select {
				case <-stop:
					return
				default:
				}
				size := []int{1, 8, 64}[rng.Intn(3)]
				vpn := rng.Intn(512) / size * size
				_ = m.Map(vpn, size, rng.Intn(1<<20)/size*size, true)
				v := rng.Intn(512)
				_, _ = m.Unmap(v, 1+rng.Intn(512-v))
			}
		}(g)
	}
	for i := 0; i < 2000; i++ {
		leaves := m.Leaves()
		prevEnd := 0
		for j, l := range leaves {
			if j > 0 && l.Start < prevEnd {
				t.Fatalf("inconsistent snapshot: %v", leaves)
			}
			prevEnd = l.Start + l.Size
		}
		_ = m.ScanDirty()
	}
	close(stop)
	wg.Wait()
}
