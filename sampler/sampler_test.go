package sampler_test

import (
	"sync"
	"testing"

	"ontology/attrib"
	"ontology/sampler"
	"ontology/tree"
)

func TestDrops(t *testing.T) {
	cases := []struct {
		name        string
		ticks       int
		busyFrom    int // [busyFrom, busyTo) is the injected busy window
		busyTo      int
		wantDropped int64
	}{
		{"137 of 1000 dropped", 1000, 500, 637, 137},
		{"no busy window", 100, 0, 0, 0},
		{"all busy", 50, 0, 50, 50},
		{"single tick busy", 10, 3, 4, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New()
			now := int64(0)
			tick := 0
			sm := sampler.New(sampler.Config{
				Interval: 10,
				Clock:    func() int64 { now += 10; return now },
				Source:   func() []string { return []string{"main", "work"} },
				Busy:     func() bool { return tick >= tc.busyFrom && tick < tc.busyTo },
			}, tr)
			for ; tick < tc.ticks; tick++ {
				sm.Tick()
			}
			st := sm.Stats()
			if st.Dropped != tc.wantDropped {
				t.Fatalf("dropped=%d want %d", st.Dropped, tc.wantDropped)
			}
			if tr.Samples()+st.Dropped+st.Invalid != st.Expected {
				t.Fatalf("identity C broken: %d+%d+%d != expected %d",
					tr.Samples(), st.Dropped, st.Invalid, st.Expected)
			}
			if got := tr.Snapshot().SelfSum(); got != tr.Samples() {
				t.Fatalf("self-sum %d != samples %d", got, tr.Samples())
			}
		})
	}
}

func TestClockAnomalies(t *testing.T) {
	cases := []struct {
		name          string
		deltas        []int64
		wantAnomalous int64
		wantExpected  int64
	}{
		{"steady clock", []int64{0, 10, 10, 10}, 0, 4},
		{"rollback skipped", []int64{0, 10, -50, 10, 10}, 1, 4},
		{"huge jump skipped", []int64{0, 10, 100000, 10}, 1, 3},
		{"repeated rollback", []int64{0, -1, -1, 10}, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := tree.New()
			clock := int64(1000)
			deltas := append([]int64(nil), tc.deltas...)
			sm := sampler.New(sampler.Config{
				Interval: 10,
				Clock:    func() int64 { clock += deltas[0]; deltas = deltas[1:]; return clock },
				Source:   func() []string { return []string{"main"} },
			}, tr)
			for range tc.deltas {
				sm.Tick()
			}
			st := sm.Stats()
			if st.Anomalous != tc.wantAnomalous || st.Expected != tc.wantExpected {
				t.Fatalf("stats=%+v want anomalous=%d expected=%d",
					st, tc.wantAnomalous, tc.wantExpected)
			}
			if tr.Snapshot().SelfSum() != st.Expected {
				t.Fatalf("identity broken after rollback: self-sum != expected %d", st.Expected)
			}
		})
	}
}

func TestInvalidAndStop(t *testing.T) {
	tr := tree.New()
	now := int64(0)
	empty := false
	sm := sampler.New(sampler.Config{
		Interval: 10,
		Clock:    func() int64 { now += 10; return now },
		Source: func() []string {
			if empty {
				return nil
			}
			return []string{"main"}
		},
	}, tr)
	sm.Tick()
	empty = true
	sm.Tick()
	sm.Tick()
	st := sm.Stats()
	if st.Invalid != 2 || tr.Samples() != 1 {
		t.Fatalf("invalid=%d samples=%d, want 2 and 1", st.Invalid, tr.Samples())
	}
	sm.Stop()
	sm.Stop()
	sm.Stop() // idempotent
	if !sm.Stopped() {
		t.Fatal("not stopped")
	}
	before := sm.Stats()
	sm.Tick()
	if sm.Stats() != before {
		t.Fatal("Tick after Stop must be a no-op")
	}
}

func TestConcurrentSampling(t *testing.T) {
	tr := tree.New()
	now := int64(0)
	var clockMu sync.Mutex
	sm := sampler.New(sampler.Config{
		Interval: 10,
		Clock: func() int64 {
			clockMu.Lock()
			defer clockMu.Unlock()
			now += 10
			return now
		},
		Source: func() []string { return []string{"main", "work"} },
	}, tr)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			sm.Tick()
		}
	}()
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				snap := tr.Snapshot()
				if snap.SelfSum() != snap.Samples {
					t.Error("observed half-updated tree")
					return
				}
				attrib.NewReport(snap).BySelf()
			}
		}()
	}
	wg.Wait()
	sm.Stop()
	st := sm.Stats()
	if tr.Samples()+st.Dropped+st.Invalid != st.Expected {
		t.Fatalf("identity C broken: %+v samples=%d", st, tr.Samples())
	}
}
