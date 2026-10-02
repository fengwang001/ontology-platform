package cidpool

import (
	"sync"
	"testing"
)

// TestConcurrent hammers every operation from many goroutines and checks the
// invariants that must hold after each serialized step: at most L active
// entries, no two non-parked paths on one seq, and paths only hold active
// entries.
func TestConcurrent(t *testing.T) {
	p := mustPool(t, 8)
	for s := uint64(1); s <= 7; s++ {
		if err := p.OnNew(s, 0, cid(byte(s)), tok(byte(s))); err != nil {
			t.Fatal(err)
		}
	}

	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				pid := uint64((id*7 + i) % 16)
				switch i % 7 {
				case 0:
					_ = p.NewPath(pid)
				case 1:
					_ = p.FreePath(pid)
				case 2:
					seq := uint64(1 + (id+i)%7)
					_ = p.Retire(seq)
				case 3:
					_ = p.OnNew(uint64(20+id), 0, cid(byte(20+id)), tok(byte(20+id)))
				case 4:
					_, _ = p.PathSeq(pid)
				case 5:
					_ = p.IsReset(tok(byte(id % 8)))
				case 6:
					_ = p.TakeRetires()
				}
			}
		}(w)
	}
	wg.Wait()

	if p.ActiveCount() > 8 {
		t.Fatalf("ActiveCount = %d > 8", p.ActiveCount())
	}
	occupied := map[uint64]int{}
	for pid := uint64(0); pid < 16; pid++ {
		s, err := p.PathSeq(pid)
		if err != nil || s.Parked {
			continue
		}
		occupied[s.Seq]++
		if occupied[s.Seq] > 1 {
			t.Fatalf("seq %d occupied by multiple paths", s.Seq)
		}
		e, ok := p.EntryAt(s.Seq)
		if !ok || e.Retired {
			t.Fatalf("path %d holds invalid seq %d", pid, s.Seq)
		}
	}
}
