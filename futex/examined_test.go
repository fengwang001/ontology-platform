package futex

import "testing"

// TestExaminedCounters verifies the documented inspection bounds at 1e5 scale:
// Wake touches at most n plus skipped mismatches and nothing on other
// addresses; Requeue touches at most nWake+nRequeue; Advance touches at most
// the number of timeouts plus one.
func TestExaminedCounters(t *testing.T) {
	const N = 100_000
	f := New(N + 10)
	f.Store(7, 0)
	f.Store(8, 0)

	// 1e5 waiters spread across many addresses with distinct deadlines.
	for i := 0; i < N; i++ {
		addr := int64(i % 1000)
		bitset := uint32(0x2)
		if i%2 == 0 {
			bitset = 0x1
		}
		mustWait(t, f, i, addr, 0, bitset, i%100, int64(1_000_000+i))
	}
	// A few waiters at dedicated addresses with controlled bitsets.
	for i := 0; i < 4; i++ {
		bs := uint32(0x1)
		if i == 1 || i == 3 {
			bs = 0x2
		}
		mustWait(t, f, N+i, 7, 0, bs, 0, 0)
	}
	for i := 0; i < 3; i++ {
		mustWait(t, f, N+10+i, 8, 0, 0x1, 0, 0)
	}

	f.ResetExamined()

	// Wake(addr=7, n=1, bitset=0x2): scans tid N (mismatch), then tid N+1.
	w, err := f.Wake(7, 1, 0x2)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "wake", w, []int{N + 1})
	if got := f.Examined()["wake"]; got != 2 {
		t.Fatalf("wake examined=%d, want 2 (1 wake + 1 skip)", got)
	}

	// Wake with n=0 examines nothing.
	if _, err := f.Wake(7, 0, 0x1); err != nil {
		t.Fatal(err)
	}
	if got := f.Examined()["wake"]; got != 2 {
		t.Fatalf("wake n=0 examined=%d, want still 2", got)
	}

	// Requeue examines at most nWake+nRequeue regardless of queue length.
	_, rq, err := f.Requeue(8, 99, 1, 1, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	eqInts(t, "requeue moved", rq, []int{N + 11})
	if got := f.Examined()["requeue"]; got != 2 {
		t.Fatalf("requeue examined=%d, want nWake+nRequeue=2", got)
	}

	// Advance: expire exactly 3 waiters, examines at most 3+1.
	ex, err := f.Advance(1_000_002)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex) != 3 {
		t.Fatalf("expired=%d, want 3: %v", len(ex), ex)
	}
	if ex[0] != 0 || ex[1] != 1 || ex[2] != 2 {
		t.Fatalf("expired order=%v", ex)
	}
	if got := f.Examined()["advance"]; got > 4 {
		t.Fatalf("advance examined=%d, want <= timeouts+1=4", got)
	}

	// No more due waiters: advancing again peeks at exactly one heap top.
	f.ResetExamined()
	if _, err := f.Advance(1_000_002); err != nil {
		t.Fatal(err)
	}
	if got := f.Examined()["advance"]; got != 1 {
		t.Fatalf("idle advance examined=%d, want 1", got)
	}
}
