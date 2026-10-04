package tracker_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/tracker"
)

func mustNew(t *testing.T, primary string, reps ...string) *tracker.Group {
	t.Helper()
	g, err := tracker.New(primary, reps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func eq(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name    string
		primary string
		reps    []string
		want    error
	}{
		{"empty primary", "", nil, tracker.ErrInvalidArgument},
		{"long primary", string(make([]byte, 65)), nil, tracker.ErrInvalidArgument},
		{"dup replica", "P", []string{"R", "R"}, tracker.ErrInvalidArgument},
		{"replica equals primary", "P", []string{"P"}, tracker.ErrInvalidArgument},
		{"too many", "P", names(16), tracker.ErrInvalidArgument},
		{"ok", "P", names(15), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := tracker.New(tc.primary, tc.reps)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tc.want == nil && g == nil {
				t.Fatal("expected group")
			}
		})
	}
}

func names(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "R" + itoa(i+1)
	}
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestConfirmationBeforeGCP reproduces the worked example: seq 3 confirms
// while seq 2 is unconfirmed and gcp stays at 1.
func TestConfirmationBeforeGCP(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		if _, err := g.Write("b"); err != nil {
			t.Fatal(err)
		}
	}
	acks := []struct {
		m    string
		seq  int64
		term int64
	}{
		{"R1", 1, 1}, {"R1", 3, 1}, {"R2", 1, 1}, {"R2", 2, 1}, {"R2", 3, 1},
	}
	for _, a := range acks {
		if err := g.Ack(a.m, a.seq, a.term); err != nil {
			t.Fatalf("Ack %v: %v", a, err)
		}
	}
	if g.GCP() != 1 {
		t.Fatalf("gcp = %d, want 1", g.GCP())
	}
	if got := g.Confirmed(); !eq(got, []int64{1, 3}) {
		t.Fatalf("confirmed = %v, want [1 3]", got)
	}
	lcp, _ := g.LCP("R1")
	if lcp != 1 {
		t.Fatalf("R1 lcp = %d, want 1", lcp)
	}
	lcp, _ = g.LCP("R2")
	if lcp != 3 {
		t.Fatalf("R2 lcp = %d, want 3", lcp)
	}
}

// TestFailLaggingReplica: removing the laggard jumps gcp and appends the
// backlog confirmation in ascending order.
func TestFailLaggingReplica(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		g.Write("b")
	}
	g.Ack("R1", 1, 1)
	g.Ack("R1", 3, 1)
	for _, seq := range []int64{1, 2, 3} {
		g.Ack("R2", seq, 1)
	}
	if err := g.FailReplica("R1"); err != nil {
		t.Fatal(err)
	}
	if g.GCP() != 3 {
		t.Fatalf("gcp = %d, want 3", g.GCP())
	}
	if got := g.Confirmed(); !eq(got, []int64{1, 3, 2}) {
		t.Fatalf("confirmed = %v, want [1 3 2]", got)
	}
	if _, err := g.LCP("R1"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("LCP after fail err = %v", err)
	}
}

func TestRepeatedAckIdempotent(t *testing.T) {
	g := mustNew(t, "P", "R1")
	g.Write("a")
	g.Write("b")
	if err := g.Ack("R1", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.Ack("R1", 1, 1); err != nil {
		t.Fatalf("repeat ack: %v", err)
	}
	if g.GCP() != 1 {
		t.Fatalf("gcp = %d", g.GCP())
	}
}

func TestAckRejectionOrder(t *testing.T) {
	g := mustNew(t, "P", "R1")
	g.Write("a")

	// Invalid argument beats everything.
	if err := g.Ack("R1", 5, 1); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("seq out of range: %v", err)
	}
	if err := g.Ack("R1", 0, 1); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("seq 0: %v", err)
	}
	if err := g.Ack("R1", 1, 9); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("future term: %v", err)
	}
	// Unknown member precedes stale term.
	if err := g.Ack("Ghost", 1, 0); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("unknown member: %v", err)
	}
	// Stale term precedes state mismatch (ack to primary at old term).
	if err := g.Ack("P", 1, 0); !errors.Is(err, tracker.ErrStaleTerm) {
		t.Fatalf("primary stale ack: %v", err)
	}
	// State mismatch: acking the primary at the current term.
	if err := g.Ack("P", 1, 1); !errors.Is(err, tracker.ErrState) {
		t.Fatalf("primary ack: %v", err)
	}
}

func TestCatchupJoinCondition(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	for i := 0; i < 3; i++ {
		g.Write("b")
	}
	g.Ack("R1", 1, 1)
	g.Ack("R1", 3, 1)
	for _, seq := range []int64{1, 2, 3} {
		g.Ack("R2", seq, 1)
	}
	// gcp = 1, confirmed = {1,3}.
	if err := g.AddReplica("R3"); err != nil {
		t.Fatal(err)
	}
	if err := g.Ack("R3", 1, 1); err != nil {
		t.Fatal(err)
	}
	// lcp == gcp == 1, but confirmed seq 3 not processed: still behind.
	if err := g.MarkInSync("R3"); !errors.Is(err, tracker.ErrCaughtUp) {
		t.Fatalf("MarkInSync without confirmed ops: %v", err)
	}
	if err := g.Ack("R3", 3, 1); err != nil {
		t.Fatal(err)
	}
	if err := g.MarkInSync("R3"); err != nil {
		t.Fatalf("MarkInSync full confirmed: %v", err)
	}
	if g.GCP() != 1 {
		t.Fatalf("gcp after join = %d, want 1 (R3 has a hole at 2)", g.GCP())
	}

	// MarkInSync rejection ordering.
	if err := g.MarkInSync(""); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("bad name: %v", err)
	}
	if err := g.MarkInSync("Ghost"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("unknown: %v", err)
	}
	if err := g.MarkInSync("R1"); !errors.Is(err, tracker.ErrState) {
		t.Fatalf("sync member: %v", err)
	}
}

func TestFailReplicaRejections(t *testing.T) {
	g := mustNew(t, "P", "R1")
	if err := g.FailReplica(""); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("bad name: %v", err)
	}
	if err := g.FailReplica("Ghost"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("unknown: %v", err)
	}
	if err := g.FailReplica("P"); !errors.Is(err, tracker.ErrState) {
		t.Fatalf("fail primary: %v", err)
	}
}

func TestAddReplicaRejections(t *testing.T) {
	g := mustNew(t, "P")
	if err := g.AddReplica(""); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("bad name: %v", err)
	}
	if err := g.AddReplica("P"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("existing: %v", err)
	}
}

func TestSoloPrimaryConfirmsImmediately(t *testing.T) {
	g := mustNew(t, "P")
	for i := 0; i < 3; i++ {
		g.Write("b")
	}
	if g.GCP() != 3 {
		t.Fatalf("gcp = %d, want 3", g.GCP())
	}
	if got := g.Confirmed(); !eq(got, []int64{1, 2, 3}) {
		t.Fatalf("confirmed = %v", got)
	}
	h, err := g.History("P")
	if err != nil || len(h) != 3 || h[2].Term != 1 {
		t.Fatalf("history = %v err=%v", h, err)
	}
	if _, err := g.History("X"); !errors.Is(err, tracker.ErrMember) {
		t.Fatalf("history unknown: %v", err)
	}
}

func TestAddReplicaLimit(t *testing.T) {
	g := mustNew(t, "P")
	for i := 0; i < 15; i++ {
		if err := g.AddReplica("R" + itoa(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := g.AddReplica("R16"); !errors.Is(err, tracker.ErrInvalidArgument) {
		t.Fatalf("17th member: %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	g := mustNew(t, "P", "R1", "R2")
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				switch i % 5 {
				case 0:
					g.Write("x")
				case 1:
					g.Ack("R1", int64(1+i%30), g.Term())
				case 2:
					g.Ack("R2", int64(1+i%30), g.Term())
				case 3:
					g.GCP()
				case 4:
					g.Confirmed()
				}
			}
		}(w)
	}
	wg.Wait()
	// After all acks drain, no panic and state is internally consistent.
	if g.GCP() < 0 {
		t.Fatal("negative gcp")
	}
}

// TestStepsBoundCrossMembers proves the non-exported counter property: with
// 100 and 10000 operations acked in reverse seq order, total lcp forward
// steps across members stay within writes + accepted first acks (linear),
// where a naive rescan-from-1 implementation would be quadratic.
func TestStepsBoundCrossMembers(t *testing.T) {
	for _, n := range []int64{100, 10000} {
		g := mustNew(t, "P", "R1", "R2")
		for i := int64(0); i < n; i++ {
			g.Write("b")
		}
		for seq := n; seq >= 1; seq-- {
			g.Ack("R1", seq, 1)
		}
		// R2 acks half, also reverse.
		for seq := n / 2; seq >= 1; seq-- {
			g.Ack("R2", seq, 1)
		}
		// Duplicate acks must not add steps.
		for seq := n; seq >= 1; seq-- {
			g.Ack("R1", seq, 1)
		}
		writes := n
		firstAcks := n + n/2
		if got := g.Steps(); got > writes+firstAcks {
			t.Fatalf("n=%d steps=%d > bound=%d", n, got, writes+firstAcks)
		}
		if lcp, _ := g.LCP("R1"); lcp != n {
			t.Fatalf("n=%d R1 lcp=%d", n, lcp)
		}
	}
}
