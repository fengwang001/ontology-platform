package reclaim

import (
	"sync"
	"testing"

	"ontology/snapshot"
	"ontology/txid"
)

func TestWatermarkMonotonicAndExamined(t *testing.T) {
	src := txid.NewSourceAt(1)
	reg := snapshot.NewRegistry(src, 0)
	r := New(reg)

	var mu sync.Mutex
	visits := map[string]int{}
	prune := func(key string, wm txid.ID) int {
		mu.Lock()
		visits[key]++
		mu.Unlock()
		return 2
	}

	// Transactions 1..5 commit, no snapshots: watermark follows next id.
	for i := 0; i < 5; i++ {
		_, _ = src.Allocate()
	}
	r.Notify("a")
	r.Notify("a") // coalesced
	r.Notify("b")
	if r.Queued() != 2 {
		t.Fatalf("queued = %d, want 2", r.Queued())
	}
	rep := r.Advance(prune, 0)
	if rep.Candidates != 2 || rep.Examined != 4 {
		t.Fatalf("report = %+v", rep)
	}
	if !rep.Watermark.Valid() || rep.Watermark != 6 {
		t.Fatalf("watermark = %d, want 6", rep.Watermark)
	}
	if len(visits) != 2 {
		t.Fatalf("visited %v", visits)
	}

	// Recompute cannot move it backwards (ids still 6 next).
	rep2 := r.Advance(prune, 0)
	if rep2.Watermark.Less(rep.Watermark) {
		t.Fatal("watermark moved backwards")
	}
}

func TestWatermarkPinnedByOldestSnapshot(t *testing.T) {
	src := txid.NewSourceAt(1)
	reg := snapshot.NewRegistry(src, 0)
	r := New(reg)

	_, _ = src.Allocate() // 1
	old, _ := reg.Open(nil)
	for i := 0; i < 10; i++ {
		_, _ = src.Allocate()
	}
	rep := r.Advance(func(string, txid.ID) int { return 0 }, 0)
	if rep.Watermark != 2 {
		t.Fatalf("pinned watermark = %d, want 2", rep.Watermark)
	}
	reg.Close(old)
	rep = r.Advance(func(string, txid.ID) int { return 0 }, 0)
	if rep.Watermark != 12 {
		t.Fatalf("advanced watermark = %d, want 12", rep.Watermark)
	}
}
