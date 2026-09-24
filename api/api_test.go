package api

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// runOps applies ops to s: op{et, ts} with ts >= 0 is Heartbeat(ts),
// otherwise Ingest(et, ids[i]).
func runOps(t *testing.T, s *System, ops [][2]int64, ids []string) {
	t.Helper()
	for i, op := range ops {
		var err error
		if op[1] >= 0 {
			err = s.Heartbeat(op[1])
		} else {
			_, err = s.Ingest(op[0], ids[i])
		}
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestNaiveReplayConsistency(t *testing.T) {
	for _, tc := range []struct{ seed int64; n int }{{1, 50}, {7, 200}, {9, 1000}, {13, 5000}} {
		r := rand.New(rand.NewSource(tc.seed))
		var ops [][2]int64
		var ids []string
		hb := int64(0)
		for i := 0; i < tc.n; i++ {
			ids = append(ids, fmt.Sprintf("id%d", i))
			if r.Intn(4) == 0 {
				hb += r.Int63n(50) // monotonic heartbeats
				ops = append(ops, [2]int64{0, hb})
			} else {
				ops = append(ops, [2]int64{r.Int63n(100) - 10, -1})
			}
		}
		s, err := New(5)
		if err != nil {
			t.Fatal(err)
		}
		runOps(t, s, ops, ids)
		wantView, wantLate := naive(5, ops, ids)
		got := s.View()
		if s.LateCount() != wantLate || len(got) != len(wantView) {
			t.Fatalf("seed=%d: late=%d/%d len=%d/%d", tc.seed, s.LateCount(), wantLate, len(got), len(wantView))
		}
		for i := range got {
			if got[i] != wantView[i] {
				t.Fatalf("seed=%d view[%d]=%v, want %v", tc.seed, i, got[i], wantView[i])
			}
		}
	}
}

func TestLateOnlyByETW(t *testing.T) {
	// Same event stream, wildly different PTW: views must be identical.
	var views []string
	for _, hb := range []int64{-1, 100, 1 << 40} {
		s, _ := New(5)
		s.Ingest(10, "a")
		if hb >= 0 {
			s.Heartbeat(hb)
		}
		for _, op := range []struct {
			et int64
			id string
			ok bool
		}{{9, "b", true}, {5, "c", false}, {6, "d", true}, {4, "e", false}} {
			ok, _ := s.Ingest(op.et, op.id)
			if ok != op.ok {
				t.Fatalf("hb=%d et=%d: ok=%v, want %v", hb, op.et, ok, op.ok)
			}
		}
		views = append(views, fmt.Sprint(s.View()))
	}
	if views[0] != views[1] || views[1] != views[2] {
		t.Fatalf("PTW changed the outcome: %v", views)
	}
}

func TestMonotonicIndependent(t *testing.T) {
	s, _ := New(5)
	ops := [][2]int64{{10, -1}, {0, 100}, {8, -1}, {20, -1}, {0, 200}, {14, -1}, {16, -1}}
	ids := []string{"a", "", "b", "c", "", "d", "e"}
	lastE, lastP := s.ETW(), s.PTW()
	for i, op := range ops {
		snap := fmt.Sprint(s.ETW(), s.MaxEt(), s.LateCount(), s.View())
		if op[1] >= 0 {
			s.Heartbeat(op[1])
			if fmt.Sprint(s.ETW(), s.MaxEt(), s.LateCount(), s.View()) != snap {
				t.Fatalf("step %d: heartbeat changed event-side state", i)
			}
		} else {
			s.Ingest(op[0], ids[i])
		}
		if s.ETW() < lastE || s.PTW() < lastP {
			t.Fatalf("step %d: watermark regressed", i)
		}
		lastE, lastP = s.ETW(), s.PTW()
	}
}

func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	if _, err := New(-1); !errors.Is(err, ErrNegativeLateness) {
		t.Fatalf("New(-1): %v", err)
	}
	s, _ := New(5)
	s.Ingest(10, "a")
	s.Heartbeat(50)
	snap := fmt.Sprint(s.ETW(), s.PTW(), s.MaxEt(), s.LateCount(), s.View())
	for _, tc := range []struct {
		err  error
		want error
	}{
		{func() error { _, e := s.Ingest(11, ""); return e }(), ErrEmptyID},
		{s.Heartbeat(49), ErrHeartbeatRegression},
	} {
		if !errors.Is(tc.err, tc.want) {
			t.Fatalf("err=%v, want %v", tc.err, tc.want)
		}
		for _, other := range []error{ErrNegativeLateness, ErrEmptyID, ErrHeartbeatRegression} {
			if other != tc.want && errors.Is(tc.err, other) {
				t.Fatalf("err=%v confused with %v", tc.err, other)
			}
		}
	}
	if fmt.Sprint(s.ETW(), s.PTW(), s.MaxEt(), s.LateCount(), s.View()) != snap {
		t.Fatal("rejected op changed state")
	}
	if ok, _ := s.Ingest(11, "b"); !ok { // still usable
		t.Fatal("system unusable after rejected ops")
	}
}

func TestConcurrentIngest(t *testing.T) {
	const n = 64
	s, _ := New(5)
	done := make(chan struct{})
	var wg sync.WaitGroup
	for k := 0; k < 2; k++ { // readers: ETW/PTW must never regress
		wg.Add(1)
		go func() {
			defer wg.Done()
			lastE, lastP := s.ETW(), s.PTW()
			for {
				select {
				case <-done:
					return
				default:
				}
				if e := s.ETW(); e >= lastE {
					lastE = e
				} else {
					t.Error("ETW regressed")
				}
				if p := s.PTW(); p >= lastP {
					lastP = p
				} else {
					t.Error("PTW regressed")
				}
			}
		}()
	}
	for i := 0; i < n; i++ { // same huge et: all on time regardless of order
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if ok, _ := s.Ingest(1<<40, fmt.Sprintf("id%d", i)); !ok {
				t.Errorf("ingest %d judged late", i)
			}
			s.Heartbeat(int64(i)) // may regress: error ignored by design
		}(i)
	}
	wg.Wait()
	close(done)
	if got := len(s.View()); got != n {
		t.Fatalf("View has %d events, want %d", got, n)
	}
	if err := SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
