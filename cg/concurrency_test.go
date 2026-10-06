package cg

import (
	"sync"
	"testing"
)

// Concurrent calls must be equivalent to some serial order. Time must be
// non-decreasing for each goroutine; goroutine G uses timestamps >=1000.
func TestConcurrentSerialization(t *testing.T) {
	c := New(Config{DefaultQueueCapacity: 128, MaxFreezeHold: 1_000_000})
	mustCreate(t, c, 0, "g", "a", "b")

	var wg sync.WaitGroup
	run := func(base int64, fn func(t2 int64)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i < 200; i++ {
				fn(base + i)
			}
		}()
	}

	// Each writer owns a disjoint time window so the globally serialized
	// operations never observe a clock decrease.
	doneA := make(chan struct{})
	runW := func() {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := int64(0); i < 200; i++ {
				if _, err := c.Write(i, "a", "x"); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
			close(doneA)
		}()
	}
	runW()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-doneA
		for i := int64(0); i < 200; i++ {
			if _, err := c.Write(1000+i, "b", "x"); err != nil {
				t.Errorf("write b: %v", err)
				return
			}
		}
	}()
	wg.Wait()
	// Later, disjoint windows: snapshot lifecycle then readers.
	run(5000, func(tt int64) {
		if _, err := c.BeginSnapshot(tt, "g", tt+5); err == nil {
			_, _ = c.ConfirmFreeze(tt+1, "g", "a")
			_, _ = c.ConfirmFreeze(tt+2, "g", "b")
			_ = c.Abort(tt+3, "g")
		}
	})
	run(6000, func(tt int64) {
		_, _ = c.GetVolume(tt, "a")
		_, _ = c.GetGroup(tt, "g")
	})
	wg.Wait()

	va, err := c.GetVolume(7000, "a")
	if err != nil {
		t.Fatal(err)
	}
	vb, _ := c.GetVolume(7000, "b")
	// Every accepted write applies exactly once overall; ordering is the
	// only nondeterminism, so both volumes must reach 200 applied writes.
	if va.Seq != 200 || vb.Seq != 200 {
		t.Fatalf("applied write counts wrong: a=%d b=%d", va.Seq, vb.Seq)
	}
	st, _ := c.GetGroup(7000, "g")
	if st.Phase != Idle {
		t.Fatalf("group must finish idle, got %s", st.Phase)
	}
}

// Replaying the same accepted operation sequence yields identical seqs
// and snapshot records.
func TestDeterministicReplay(t *testing.T) {
	build := func() (*Coordinator, []func(c2 *Coordinator) (*SnapshotRecord, error)) {
		c := New(Config{DefaultQueueCapacity: 3, MaxFreezeHold: 5})
		return c, nil
	}
	_ = build
	play := func() (*Coordinator, *SnapshotRecord, []uint64) {
		c := New(Config{DefaultQueueCapacity: 3, MaxFreezeHold: 5})
		mustCreate(t, c, 0, "g", "a", "b")
		var seqs []uint64
		apply := func(vid, data string, tt int64) {
			r, err := c.Write(tt, vid, data)
			if err == nil && !r.Queued {
				seqs = append(seqs, r.Seq)
			}
		}
		apply("a", "1", 0)
		apply("b", "1", 0)
		_, _ = c.BeginSnapshot(1, "g", 4)
		_, _ = c.ConfirmFreeze(2, "g", "a")
		apply("a", "q", 2)
		apply("b", "2", 3)
		_, _ = c.ConfirmFreeze(4, "g", "b")
		rec, err := c.Commit(5, "g")
		if err != nil {
			t.Fatal(err)
		}
		apply("a", "2", 6)
		va, _ := c.GetVolume(6, "a")
		vb, _ := c.GetVolume(6, "b")
		return c, rec, []uint64{va.Seq, vb.Seq}
	}
	_, rec1, end1 := play()
	_, rec2, end2 := play()
	if end1[0] != end2[0] || end1[1] != end2[1] {
		t.Fatalf("end seqs differ: %v vs %v", end1, end2)
	}
	if rec1.Point != rec2.Point || rec1.ID != rec2.ID {
		t.Fatalf("record headers differ: %+v vs %+v", rec1, rec2)
	}
	for k := range rec1.Cutoffs {
		if rec1.Cutoffs[k] != rec2.Cutoffs[k] {
			t.Fatalf("cutoff %s differs", k)
		}
	}
}
