package ring

import (
	"errors"
	"testing"

	"ontology/change"
)

func deliverOK(t *testing.T, r *Ring, from, to int) DeliverOutcome {
	t.Helper()
	outcome, err := r.Deliver(from, to)
	if err != nil {
		t.Fatalf("Deliver(%d,%d) returned error %v", from, to, err)
	}
	return outcome
}

func writeOK(t *testing.T, r *Ring, site int, key string, op change.Op, now int64) WriteOutcome {
	t.Helper()
	outcome, err := r.Write(site, []byte(key), op, now)
	if err != nil {
		t.Fatalf("Write(%d,%s,%v,%d) returned error %v", site, key, op, now, err)
	}
	return outcome
}

func TestExampleOneConcurrentWrites(t *testing.T) {
	r, err := New(3, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if writeOK(t, r, 1, "k", change.Put{Value: 5}, 10) != WriteApplied {
		t.Fatal("first local write must apply")
	}
	if writeOK(t, r, 2, "k", change.Put{Value: 7}, 10) != WriteApplied {
		t.Fatal("second local write must apply")
	}

	if deliverOK(t, r, 1, 2) != DeliverLost {
		t.Fatal("c1 must lose by origin at site 2")
	}
	if got := pendingLen(t, r, 2, 3); got != 2 {
		t.Fatalf("2->3 length = %d, want 2", got)
	}
	if deliverOK(t, r, 1, 3) != DeliverApplied {
		t.Fatal("c1 must apply at site 3")
	}
	if deliverOK(t, r, 2, 3) != DeliverApplied {
		t.Fatal("c2 must apply at site 3")
	}
	if deliverOK(t, r, 2, 3) != DeliverDup {
		t.Fatal("re-forwarded c1 must be duplicate at site 3")
	}
}

func TestExampleTwoOlderLocalWriteStillReplicates(t *testing.T) {
	r, err := New(3, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	writeOK(t, r, 1, "k", change.Put{Value: 5}, 10)
	writeOK(t, r, 2, "k", change.Put{Value: 7}, 10)
	drainAll(t, r)

	if writeOK(t, r, 3, "k", change.Put{Value: 9}, 5) != WriteLost {
		t.Fatal("older local write must lose but still be accepted")
	}
	dequeued, dup := drainAll(t, r)
	if dequeued != 4 || dup != 2 {
		t.Fatalf("older change dequeued=%d dup=%d, want 4 and 2", dequeued, dup)
	}
	for site := 1; site <= 3; site++ {
		entry, ok, err := r.Get(site, []byte("k"))
		if err != nil || !ok || entry.Delete || entry.Value != 7 {
			t.Fatalf("site %d value = %+v ok=%v err=%v", site, entry, ok, err)
		}
		for origin := 1; origin <= 3; origin++ {
			floor, err := r.Floor(site, origin)
			if err != nil || floor != 1 {
				t.Fatalf("site %d origin %d floor=%d err=%v", site, origin, floor, err)
			}
		}
	}
}

func TestExampleThreeRingReturnCounts(t *testing.T) {
	r, err := New(4, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	writeOK(t, r, 1, "k", change.Put{Value: 5}, 1)

	steps := []struct {
		from, to int
		outcome  DeliverOutcome
	}{
		{1, 2, DeliverApplied},
		{2, 3, DeliverApplied},
		{3, 4, DeliverApplied},
		{4, 1, DeliverDup},
		{1, 4, DeliverDup},
	}
	for idx, step := range steps {
		if got := deliverOK(t, r, step.from, step.to); got != step.outcome {
			t.Fatalf("step %d got %v, want %v", idx, got, step.outcome)
		}
	}
}

func TestExampleFourDownLinkKeepsQueue(t *testing.T) {
	r, err := New(4, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	writeOK(t, r, 1, "k", change.Put{Value: 5}, 1)
	if err := r.SetLink(2, 3, false); err != nil {
		t.Fatal(err)
	}
	deliverOK(t, r, 1, 2)
	if _, err := r.Deliver(2, 3); !errors.Is(err, ErrDown) {
		t.Fatalf("down link error = %v, want ErrDown", err)
	}
	if got := pendingLen(t, r, 2, 3); got != 1 {
		t.Fatalf("down-link queue length = %d, want 1", got)
	}
	if err := r.SetLink(2, 3, true); err != nil {
		t.Fatal(err)
	}
	if got := deliverOK(t, r, 2, 3); got != DeliverApplied {
		t.Fatalf("restored deliver = %v, want applied", got)
	}
}

func pendingLen(t *testing.T, r *Ring, from, to int) int {
	t.Helper()
	pending, err := r.Pending(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return len(pending)
}

func drainAll(t *testing.T, r *Ring) (dequeued int, dup int) {
	t.Helper()
	n := r.n
	for progress := true; progress; {
		progress = false
		for from := 1; from <= n; from++ {
			for _, to := range [2]int{from%n + 1, (from-2+n)%n + 1} {
				pending, err := r.Pending(from, to)
				if err != nil {
					t.Fatal(err)
				}
				if len(pending) == 0 {
					continue
				}
				outcome, err := r.Deliver(from, to)
				if err != nil {
					t.Fatalf("draining %d->%d: %v", from, to, err)
				}
				dequeued++
				progress = true
				if outcome == DeliverDup {
					dup++
				}
			}
		}
	}
	return dequeued, dup
}
