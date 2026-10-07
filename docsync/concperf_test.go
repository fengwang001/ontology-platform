package docsync

import (
	"strings"
	"sync"
	"testing"
)

func TestConcurrentStaleChangeSingleWinner(t *testing.T) {
	st := NewStore("abcdef")
	const n = 64
	var wg sync.WaitGroup
	results := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			_, err := st.Apply(0, []Edit{{Range: rng(0, 0, 0, 0), Text: "x"}})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, stale := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if re, ok := err.(*RejectError); ok && re.Kind == RejectStale {
			stale++
		} else {
			t.Fatalf("unexpected err %v", err)
		}
	}
	if wins != 1 || stale != n-1 {
		t.Fatalf("wins=%d stale=%d", wins, stale)
	}
	if st.Version() != 1 {
		t.Fatalf("version=%d", st.Version())
	}
}

func TestConcurrentAppliesSerialize(t *testing.T) {
	st := NewStore("")
	const goroutines = 32
	const perG = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				for {
					v := st.Version()
					if _, err := st.Apply(v, []Edit{{Range: rng(0, 0, 0, 0), Text: "y"}}); err == nil {
						break
					}
				}
			}
		}()
	}
	wg.Wait()
	if got := st.Text(); got != strings.Repeat("y", goroutines*perG) {
		t.Fatalf("len=%d", len(got))
	}
}

func TestSnapshotConsistentUnderLoad(t *testing.T) {
	st := NewStore("abcdefgh")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			v := st.Version()
			_, _ = st.Apply(v, []Edit{{Range: rng(0, 0, 0, 0), Text: "z"}})
		}
	}()
	done := make(chan struct{})
	go func() {
		defer wg.Done()
		defer close(done)
		for i := 0; i < 2000; i++ {
			snap := st.Snapshot()
			for _, d := range snap.Diagnostics {
				if d.Range.End.Line >= strings.Count(snap.Text, "\n")+1 {
					t.Errorf("tearing snapshot v=%d", snap.Version)
				}
			}
		}
	}()
	<-done
	close(stop)
	wg.Wait()
}

func TestSublinearLineVisits(t *testing.T) {
	// Build a large document of many long lines.
	var sb strings.Builder
	const lines = 20000
	for i := 0; i < lines; i++ {
		sb.WriteString("0123456789")
		if i < lines-1 {
			sb.WriteByte('\n')
		}
	}
	st := NewStore(sb.String())
	st.ResetCounters()
	// Insert near the end: must not visit earlier lines.
	_, err := st.Apply(0, []Edit{{Range: rng(lines-1, 5, lines-1, 5), Text: "X"}})
	if err != nil {
		t.Fatal(err)
	}
	lineVisits := st.LineVisits()
	if lineVisits > 200 {
		t.Fatalf("insert visited %d lines, want O(log n)", lineVisits)
	}

	// Position/offset conversion near the end must also be sub-linear.
	st.ResetCounters()
	if _, err := st.OffsetOf(Position{lines - 1, 2}); err != nil {
		t.Fatal(err)
	}
	if st.LineVisits() > 200 {
		t.Fatalf("offsetOf visited %d lines", st.LineVisits())
	}
	if _, err := st.PositionOf(st.text.length() - 2); err != nil {
		t.Fatal(err)
	}
	if st.LineVisits() > 200 {
		t.Fatalf("positionOf visited %d lines", st.LineVisits())
	}
}

func TestSublinearDiagVisits(t *testing.T) {
	const diags = 20000
	var sb strings.Builder
	for i := 0; i < diags*2; i++ {
		sb.WriteByte('a')
	}
	st := NewStore(sb.String())
	// Register many diagnostics in the prefix/body.
	for i := 0; i < diags; i++ {
		off := i
		if _, err := st.Register(0, Diagnostic{
			Range: rng(0, off, 0, off+1), Message: "d",
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Insert at the very end: no diagnostic should be visited.
	st.ResetCounters()
	end := diags * 2
	if _, err := st.Apply(0, []Edit{{Range: rng(0, end, 0, end), Text: "X"}}); err != nil {
		t.Fatal(err)
	}
	if got := st.DiagVisits(); got > 200 {
		t.Fatalf("end insertion visited %d diagnostics, want O(log n)", got)
	}
}
