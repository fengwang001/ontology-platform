package mrg

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/seg"
)

// TestLocateConstant proves Append locates a session by Sid hash, not by
// scanning: the number of sessions inspected does not grow with m.
func TestLocateConstant(t *testing.T) {
	for _, m := range []int{100, 500, 1000, 5000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			mg := New()
			for i := 0; i < m; i++ {
				if err := mg.Append(fmt.Sprintf("sid-%d", i), 1, i%10); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			if err := mg.Append("sid-0", 2, 3); err != nil { // existing session
				t.Fatalf("append existing: %v", err)
			}
			if mg.checked > 2 {
				t.Fatalf("m=%d: inspected %d sessions, want O(1)", m, mg.checked)
			}
		})
	}
}

// TestUniqueIdempotent pins invariant 3: one value per (Sid, Seq); same
// value replays are idempotent, different values conflict, no trace left.
func TestUniqueIdempotent(t *testing.T) {
	mg := New()
	ops := []struct {
		sid     string
		seq, v  int
		wantErr error
	}{
		{"s", 1, 5, nil},
		{"s", 1, 5, nil}, // idempotent replay
		{"s", 1, 5, nil},
		{"s", 1, 9, seg.ErrConflict}, // same seq, different value
		{"s", 2, 7, nil},
		{"s", 2, 8, seg.ErrConflict},
	}
	for i, o := range ops {
		if err := mg.Append(o.sid, o.seq, o.v); !errors.Is(err, o.wantErr) {
			t.Fatalf("op %d: want %v, got %v", i, o.wantErr, err)
		}
	}
	if got := fmt.Sprint(mg.Seen("s")); got != "[1 2]" {
		t.Fatalf("conflicts left trace: seen=%s", got)
	}
	if err := mg.Close("s", 2); err != nil {
		t.Fatalf("close after conflicts: %v", err)
	}
	if r, closed, _ := mg.Result("s"); !closed || r != 57 {
		t.Fatalf("result = %d,%v want 57,true", r, closed)
	}
}

// TestConcurrentAppendClose: many goroutines append one session's Seq=1..N
// events; Close(N) then succeeds with the Seq-ordered fold, and a frozen
// session's result reads constant the whole time. No sleeps.
func TestConcurrentAppendClose(t *testing.T) {
	const n = 18
	mg := New()
	mg.Append("z", 1, 7)
	if err := mg.Close("z", 1); err != nil {
		t.Fatalf("close z: %v", err)
	}
	stop := make(chan struct{})
	var rg sync.WaitGroup
	rg.Add(1)
	go func() {
		defer rg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if r, closed, err := mg.Result("z"); err != nil || !closed || r != 7 {
					t.Errorf("frozen result changed: %d,%v,%v", r, closed, err)
					return
				}
			}
		}
	}()
	var wg sync.WaitGroup
	for w := 0; w < 6; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := w; i < n; i += 6 {
				if err := mg.Append("s", i+1, (i+1)%10); err != nil {
					t.Errorf("append: %v", err)
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	rg.Wait()
	if err := mg.Close("s", n); err != nil {
		t.Fatalf("close after concurrent appends: %v", err)
	}
	want := 0
	for i := 1; i <= n; i++ {
		want = want*10 + i%10
	}
	if r, closed, _ := mg.Result("s"); !closed || r != want {
		t.Fatalf("got %d,%v, want %d,true", r, closed, want)
	}
}
