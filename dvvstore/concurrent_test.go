package dvvstore

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestConcurrentMutualMergeNoDeadlock runs merges in both directions across
// many goroutines, including self-merges; the test fails if a deadlock keeps
// it from finishing.
func TestConcurrentMutualMergeNoDeadlock(t *testing.T) {
	a := mustNew(t, 8)
	b := mustNew(t, 8)
	if err := a.Put("k", "A", nil, "a"); err != nil {
		t.Fatal(err)
	}
	if err := b.Put("k", "B", nil, "b"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(3)
			go func() { defer wg.Done(); a.Merge(b) }()
			go func() { defer wg.Done(); b.Merge(a) }()
			go func() { defer wg.Done(); a.Merge(a) }()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent mutual merges deadlocked")
	}

	if got, want := a.Get("k"), b.Get("k"); !snapshotsEquivalent(got, want) {
		t.Fatalf("stores diverged after mutual merges:\n a %#v\n b %#v", got, want)
	}
	t.Logf("input: 20 goroutines each doing a<-b, b<-a, a<-a; output: no deadlock and identical state; judgment: snapshot-then-lock avoids lock cycles")
}

// TestConcurrentPutAndMerge fans pre-planned empty-context writes out across
// replicas while merges happen concurrently. Every write must be accepted,
// and after stopping the workers and fully merging, all replicas converge to
// exactly the complete set of unique dots (one per write).
func TestConcurrentPutAndMerge(t *testing.T) {
	const writersPerNode = 40

	replicas := []*Store{mustNew(t, 1000), mustNew(t, 1000), mustNew(t, 1000)}

	var wg sync.WaitGroup
	for nodeIdx, id := range []string{"A", "B", "C"} {
		for w := 0; w < writersPerNode; w++ {
			wg.Add(1)
			go func(replica int, id string, w int) {
				defer wg.Done()
				if err := replicas[replica].Put("k", id, nil, id+"-val"); err != nil {
					t.Errorf("concurrent Put(%q): %v", id, err)
				}
			}(nodeIdx, id, w)
		}
	}

	stop := make(chan struct{})
	var mergeWG sync.WaitGroup
	for i := 0; i < 6; i++ {
		mergeWG.Add(1)
		go func(i int) {
			defer mergeWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					from := (i + 1) % len(replicas)
					replicas[i%len(replicas)].Merge(replicas[from])
				}
			}
		}(i)
	}

	wg.Wait()
	close(stop)
	mergeWG.Wait()

	// Final full convergence: every ordered pair merge, twice.
	for round := 0; round < 2; round++ {
		for i := range replicas {
			for j := range replicas {
				replicas[i].Merge(replicas[j])
			}
		}
	}

	wantDots := 3 * writersPerNode
	first := replicas[0].Get("k")
	if len(first.Siblings) != wantDots {
		t.Fatalf("converged siblings = %d, want %d (one dot per successful write)", len(first.Siblings), wantDots)
	}
	seen := map[string]int64{}
	for _, sib := range first.Siblings {
		seen[sib.Dot.ID]++
	}
	if len(seen) != 3 {
		t.Fatalf("converged nodes = %v, want A,B,C", seen)
	}
	for id, count := range seen {
		if count != writersPerNode {
			t.Fatalf("node %s dots = %d, want %d", id, count, writersPerNode)
		}
	}
	for i, replica := range replicas {
		if got := replica.Get("k"); !snapshotsEquivalent(got, first) {
			t.Fatalf("replica %d did not converge:\n got %#v\n want %#v", i, got, first)
		}
	}
	t.Logf("input: %d concurrent empty-ctx Puts across 3 replicas + concurrent merges; output: %d unique converged siblings on every replica; judgment: linearizable dots, merge union", wantDots, wantDots)
}

func snapshotsEquivalent(a, b Snapshot) bool {
	if len(a.Siblings) != len(b.Siblings) || len(a.Context) != len(b.Context) {
		return false
	}
	va := map[Dot]string{}
	for _, sib := range a.Siblings {
		va[sib.Dot] = sib.Value
	}
	for _, sib := range b.Siblings {
		if va[sib.Dot] != sib.Value {
			return false
		}
	}
	for node, counter := range a.Context {
		if b.Context[node] != counter {
			return false
		}
	}
	return true
}

// TestRejectedPutConcurrentNeverMutates mixes rejected and accepted Puts and
// checks a rejected Put never removes siblings or rewinds seen.
func TestRejectedPutConcurrentNeverMutates(t *testing.T) {
	s := mustNew(t, 100)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if err := s.Put("k", "A", nil, "v"); err != nil && !errors.Is(err, ErrCapExceeded) {
				t.Errorf("unexpected err: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			_ = s.Put("k", "A", map[string]int64{"Z": 1}, "v") // always ErrContextAhead
		}()
	}
	wg.Wait()

	snap := s.Get("k")
	if snap.Context["A"] != int64(len(snap.Siblings)) {
		t.Fatalf("seen[A]=%d but siblings=%d; rejected puts must not desync seen", snap.Context["A"], len(snap.Siblings))
	}
	if len(snap.Siblings) > 100 {
		t.Fatalf("cap violated: %d siblings", len(snap.Siblings))
	}
	t.Logf("input: 100 accepted/over-cap and 100 ahead-ctx Puts concurrently; output: %d siblings, seen[A]=%d; judgment: rejects never mutate", len(snap.Siblings), snap.Context["A"])
}
