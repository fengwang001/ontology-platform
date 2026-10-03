package numa

import "testing"

func newExampleLock(t *testing.T) *TeamRWMutex {
	t.Helper()
	lock, err := New(Config{
		Nodes:      2,
		Threads:    6,
		NodeOf:     []int{0, 0, 1, 1, 0, 1},
		LocalLimit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestSpecExample(t *testing.T) {
	lock := newExampleLock(t)

	assertGranted(t, lock.WLock(0), 0)
	assertGranted(t, lock.RLock(1))
	assertGranted(t, lock.WLock(2))
	assertGranted(t, lock.WLock(3))
	assertGranted(t, lock.WLock(4))
	assertGranted(t, lock.WLock(5))

	snapshot := lock.Snapshot()
	if got := snapshot.ReadQueue; len(got) != 1 || got[0] != 1 {
		t.Fatalf("Qr=%v, want [1]", got)
	}
	if got := snapshot.GroupQueue; len(got) != 1 || got[0] != 1 {
		t.Fatalf("G=%v, want [1]", got)
	}

	assertGranted(t, lock.WUnlock(0), 1)
	snapshot = lock.Snapshot()
	if got := snapshot.GroupQueue; len(got) != 2 || got[0] != 1 || got[1] != 0 {
		t.Fatalf("G=%v, want [1 0]", got)
	}

	assertGranted(t, lock.RUnlock(1), 2)
	assertGranted(t, lock.WUnlock(2), 3)
	assertGranted(t, lock.WUnlock(3), 4)
	snapshot = lock.Snapshot()
	if snapshot.Writer != 4 || snapshot.Passes != 0 {
		t.Fatalf("writer=%d passes=%d, want writer 4 and passes 0", snapshot.Writer, snapshot.Passes)
	}
	if got := snapshot.GroupQueue; len(got) != 1 || got[0] != 1 {
		t.Fatalf("G=%v, want [1]", got)
	}
}

func assertGranted(t *testing.T, outcome Outcome, want ...int) {
	t.Helper()
	if !outcome.OK {
		t.Fatalf("call rejected: %s", outcome.Reason)
	}
	if len(outcome.Granted) != len(want) {
		t.Fatalf("granted=%v, want %v", outcome.Granted, want)
	}
	for index := range want {
		if outcome.Granted[index] != want[index] {
			t.Fatalf("granted=%v, want %v", outcome.Granted, want)
		}
	}
}
