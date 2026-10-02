package jit

import (
	"errors"
	"testing"
)

func exampleConfig() Config {
	return Config{
		N: 3, A1: 3, M1: 2, B1: 10, A2: 6, M2: 4, B2: 20,
		F: 2, Qc: 3, D1: 5, D2: 10, Pd: 100, C: 30, Kd: 2,
	}
}

func mustCall(t *testing.T, m *Manager, method int, n, now int64) int {
	t.Helper()
	tier, err := m.Call(method, n, now)
	if err != nil {
		t.Fatalf("Call(%d,%d,%d) unexpected error: %v", method, n, now, err)
	}
	return tier
}

func mustState(t *testing.T, m *Manager, method int, now int64) State {
	t.Helper()
	st, err := m.State(method, now)
	if err != nil {
		t.Fatalf("State(%d,%d) unexpected error: %v", method, now, err)
	}
	return st
}

// TestExampleWalkthrough replays the scenario from the specification.
func TestExampleWalkthrough(t *testing.T) {
	m, err := NewManager(exampleConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Method 0: two calls, no promotion.
	if got := mustCall(t, m, 0, 0, 1); got != 0 {
		t.Fatalf("now=1 tier = %d, want 0", got)
	}
	if got := mustCall(t, m, 0, 0, 2); got != 0 {
		t.Fatalf("now=2 tier = %d, want 0", got)
	}
	// now=3: i=3 meets H1, enqueue tier 1, start 3 finish 8, LF=8.
	if got := mustCall(t, m, 0, 0, 3); got != 0 {
		t.Fatalf("now=3 tier = %d, want 0", got)
	}
	st := mustState(t, m, 0, 3)
	if st.QueueLen != 1 || st.LF != 8 {
		t.Fatalf("after now=3: QueueLen=%d LF=%d, want 1/8", st.QueueLen, st.LF)
	}
	// now=4: inflight, no promotion check, still tier 0.
	if got := mustCall(t, m, 0, 0, 4); got != 0 {
		t.Fatalf("now=4 tier = %d, want 0", got)
	}
	// now=8: job installs exactly at its finish time, call runs at tier 1.
	if got := mustCall(t, m, 0, 0, 8); got != 1 {
		t.Fatalf("now=8 tier = %d, want 1", got)
	}
	// now=9: i=6 meets H2, enqueue tier 2, start max(9,8)=9 finish 19.
	if got := mustCall(t, m, 0, 0, 9); got != 1 {
		t.Fatalf("now=9 tier = %d, want 1", got)
	}
	st = mustState(t, m, 0, 9)
	if st.QueueLen != 1 || st.LF != 19 {
		t.Fatalf("after now=9: QueueLen=%d LF=%d, want 1/19", st.QueueLen, st.LF)
	}

	// Method 1: back-edge driven H1 via the M1/B1 branch.
	if got := mustCall(t, m, 1, 8, 10); got != 0 {
		t.Fatalf("m1 now=10 tier = %d, want 0", got)
	}
	// now=11: i=2, b=16, q=1 so s=1; i>=M1 and i+b=18>=B1 -> tier 1 job,
	// start max(11,19)=19 finish 24, LF=24.
	if got := mustCall(t, m, 1, 8, 11); got != 0 {
		t.Fatalf("m1 now=11 tier = %d, want 0", got)
	}
	st = mustState(t, m, 1, 11)
	if st.QueueLen != 2 || st.LF != 24 {
		t.Fatalf("after m1 now=11: QueueLen=%d LF=%d, want 2/24", st.QueueLen, st.LF)
	}

	// Method 2: q=2 makes s=2, so i=3 no longer meets H1 (needs i>=6).
	for _, now := range []int64{12, 13, 14} {
		if got := mustCall(t, m, 2, 0, now); got != 0 {
			t.Fatalf("m2 now=%d tier = %d, want 0", now, got)
		}
	}
	st = mustState(t, m, 2, 14)
	if st.QueueLen != 2 || st.Tier != 0 || st.I != 3 {
		t.Fatalf("m2 after now=14: %+v, want tier 0 i=3 qlen 2", st)
	}

	// now=19: method 0's tier-2 job installs exactly at finish, returns 2.
	if got := mustCall(t, m, 0, 0, 19); got != 2 {
		t.Fatalf("m0 now=19 tier = %d, want 2", got)
	}

	// Deopt method 0 at now=20: tier 0, counters cleared, dc=1, cu=50.
	if err := m.Deopt(0, 20); err != nil {
		t.Fatalf("Deopt(0,20): %v", err)
	}
	st = mustState(t, m, 0, 20)
	if st.Tier != 0 || st.I != 0 || st.B != 0 || st.Dc != 1 || st.Cu != 50 {
		t.Fatalf("after deopt: %+v, want tier0 i0 b0 dc1 cu50", st)
	}

	// Cooldown: counters accumulate but no promotion while now < cu.
	for _, now := range []int64{21, 22, 23} {
		if got := mustCall(t, m, 0, 0, now); got != 0 {
			t.Fatalf("m0 now=%d tier = %d, want 0", now, got)
		}
	}
	st = mustState(t, m, 0, 23)
	if st.I != 3 || st.QueueLen != 1 {
		t.Fatalf("m0 after now=23: %+v, want i=3 qlen=1 (no enqueue in cooldown)", st)
	}

	// now=50: method 1's job installs first; i=4 meets H1; now>=cu so the
	// tier-1 job enqueues with start max(50,24)=50 finish 55.
	if got := mustCall(t, m, 0, 0, 50); got != 0 {
		t.Fatalf("m0 now=50 tier = %d, want 0", got)
	}
	st = mustState(t, m, 0, 50)
	if st.QueueLen != 1 || st.LF != 55 {
		t.Fatalf("after now=50: QueueLen=%d LF=%d, want 1/55", st.QueueLen, st.LF)
	}
	st = mustState(t, m, 1, 50)
	if st.Tier != 1 {
		t.Fatalf("m1 tier after install view = %d, want 1", st.Tier)
	}

	// now=250: method 0's job installs; method 2 decays by g=2 epochs:
	// i=3 >> 2 = 0, then this call makes i=1.
	if got := mustCall(t, m, 2, 0, 250); got != 0 {
		t.Fatalf("m2 now=250 tier = %d, want 0", got)
	}
	st = mustState(t, m, 2, 250)
	if st.I != 1 || st.B != 0 {
		t.Fatalf("m2 after decay: i=%d b=%d, want 1/0", st.I, st.B)
	}
	st = mustState(t, m, 0, 250)
	if st.Tier != 1 {
		t.Fatalf("m0 tier after install view = %d, want 1", st.Tier)
	}
}

// TestTier2Ban covers the second deopt reaching Kd and banning tier 2.
func TestTier2Ban(t *testing.T) {
	m, err := NewManager(exampleConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// Promote method 0 to tier 1 then tier 2.
	mustCall(t, m, 0, 0, 1)
	mustCall(t, m, 0, 0, 2)
	mustCall(t, m, 0, 0, 3) // enqueues tier 1, finish 8
	mustCall(t, m, 0, 0, 8) // installs tier 1
	mustCall(t, m, 0, 0, 9)
	mustCall(t, m, 0, 0, 10)
	mustCall(t, m, 0, 0, 11) // i=6 meets H2, enqueues tier 2, finish 21
	if got := mustCall(t, m, 0, 0, 21); got != 2 {
		t.Fatalf("tier = %d, want 2", got)
	}
	if err := m.Deopt(0, 22); err != nil {
		t.Fatalf("first Deopt: %v", err)
	}
	// dc=1: H1 fires first during the climb (i=3 at now=55, tier-1 job
	// installs at now=60); H2 then needs i >= A2*d*s = 12 with s=1.
	for now := int64(53); now <= 63; now++ { // cu = 22+30 = 52
		mustCall(t, m, 0, 0, now)
	}
	st := mustState(t, m, 0, 63)
	if st.Tier != 1 || st.I != 11 {
		t.Fatalf("before H2 threshold: %+v", st)
	}
	mustCall(t, m, 0, 0, 64) // i=12 meets H2 (d=2), enqueues tier 2
	st = mustState(t, m, 0, 64)
	if st.QueueLen != 1 {
		t.Fatalf("expected tier-2 job enqueued, state %+v", st)
	}
	if got := mustCall(t, m, 0, 0, 74); got != 2 { // finish = 64+10 = 74
		t.Fatalf("tier = %d, want 2", got)
	}
	if err := m.Deopt(0, 75); err != nil {
		t.Fatalf("second Deopt: %v", err)
	}
	st = mustState(t, m, 0, 75)
	if st.Dc != 2 || st.Cu != 75+30*2 {
		t.Fatalf("after second deopt: %+v, want dc=2 cu=135", st)
	}
	// dc=2 >= Kd=2: tier 2 banned. Climb to tier 1 at cu.
	for now := int64(136); now <= 137; now++ { // cu = 135
		mustCall(t, m, 0, 0, now)
	}
	mustCall(t, m, 0, 0, 138) // i=3 meets H1, enqueues tier 1, finish 143
	if got := mustCall(t, m, 0, 0, 143); got != 1 {
		t.Fatalf("tier = %d, want 1", got)
	}
	// At tier 1 with dc>=Kd, H2 never fires: no promotion ever again.
	for now := int64(144); now <= 200; now++ {
		if got := mustCall(t, m, 0, 0, now); got != 1 {
			t.Fatalf("tier = %d at now=%d, want 1 (tier 2 banned)", got, now)
		}
	}
	st = mustState(t, m, 0, 200)
	if st.QueueLen != 0 || st.Tier != 1 {
		t.Fatalf("banned method promoted: %+v", st)
	}
}

// TestRejections covers error ordering and no-mutation guarantees.
func TestRejections(t *testing.T) {
	m, err := NewManager(exampleConfig())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	mustCall(t, m, 0, 0, 10)

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"bad method", func() error { _, e := m.Call(3, 0, 11); return e }, ErrInvalidArgument},
		{"negative method", func() error { _, e := m.Call(-1, 0, 11); return e }, ErrInvalidArgument},
		{"bad n", func() error { _, e := m.Call(0, 1000001, 11); return e }, ErrInvalidArgument},
		{"negative n", func() error { _, e := m.Call(0, -1, 11); return e }, ErrInvalidArgument},
		{"negative now", func() error { _, e := m.Call(0, 0, -1); return e }, ErrInvalidArgument},
		{"now too large", func() error { _, e := m.Call(0, 0, 1000000000000001); return e }, ErrInvalidArgument},
		{"clock regression", func() error { _, e := m.Call(0, 0, 9); return e }, ErrClockRegression},
		{"state clock regression", func() error { _, e := m.State(0, 9); return e }, ErrClockRegression},
		{"deopt not tier2", func() error { return m.Deopt(0, 11) }, ErrNotTier2},
		{"deopt bad method", func() error { return m.Deopt(7, 11) }, ErrInvalidArgument},
		{"deopt clock regression", func() error { return m.Deopt(0, 5) }, ErrClockRegression},
	}
	for _, tc := range cases {
		if got := tc.call(); !errors.Is(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
	// Rejections must not advance T nor mutate state.
	if m.tNow != 10 {
		t.Fatalf("T advanced by rejected op: %d", m.tNow)
	}
	st := mustState(t, m, 0, 10)
	if st.I != 1 || st.Tier != 0 {
		t.Fatalf("state mutated by rejected ops: %+v", st)
	}
	// A deopt of a method with an inflight job (still tier 0) is rejected and
	// must not install anything.
	mustCall(t, m, 0, 0, 11)
	mustCall(t, m, 0, 0, 12) // i=3 meets H1, enqueues tier 1 finish 17
	if err := m.Deopt(0, 17); !errors.Is(err, ErrNotTier2) {
		t.Fatalf("deopt with inflight job: got %v, want ErrNotTier2", err)
	}
	st = mustState(t, m, 0, 17)
	if st.Tier != 1 {
		t.Fatalf("rejected deopt installed jobs: %+v", st)
	}
	// The job is still queued; a later accepted call installs it.
	if got := mustCall(t, m, 0, 0, 18); got != 1 {
		t.Fatalf("tier = %d, want 1 (job installed on accepted call)", got)
	}
}
