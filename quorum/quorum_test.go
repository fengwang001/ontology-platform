package quorum

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

type bufLogger struct{ buf bytes.Buffer }

func (l *bufLogger) Printf(format string, args ...any) {
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func newTestArbiter(t *testing.T, nodes []string) (*Arbiter, *bufLogger) {
	t.Helper()
	a, err := New(nodes)
	if err != nil {
		t.Fatalf("New(%v): %v", nodes, err)
	}
	l := &bufLogger{}
	a.SetLogger(l)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("operation log:\n%s", l.buf.String())
		}
	})
	return a, l
}

// Commit 5 replicated on the shared old majority cannot commit under the
// joint configuration because the new set has no majority. Taking a majority
// of the union would wrongly admit it.
func TestJointRequiresBothMajorities(t *testing.T) {
	a, l := newTestArbiter(t, []string{"1", "2", "3"})

	if err := a.BeginJoint([]string{"3", "4", "5"}, 1); err != nil {
		t.Fatalf("BeginJoint: %v", err)
	}

	for _, node := range []string{"1", "2", "3"} {
		if commit, err := a.Ack(node, 5); err != nil || commit != 0 {
			t.Fatalf("Ack(%s,5) = %d, %v; want 0, nil", node, commit, err)
		}
	}

	if got := a.Commit(); got != 0 {
		t.Fatalf("commit = %d; joint config with only old majority must not commit 5", got)
	}

	// New set has shared node 3 already at 5, so node 4 completes its
	// 2-of-3 majority; node 5 is not needed.
	commit, err := a.Ack("4", 5)
	if err != nil || commit != 5 {
		t.Fatalf("Ack(4,5) = %d, %v; want 5 once both 2-of-3 majorities agree", commit, err)
	}
	if !bytes.Contains(l.buf.Bytes(), []byte("majority of old AND majority of new")) {
		t.Fatalf("log missing joint-quorum rationale:\n%s", l.buf.String())
	}
}

// A node belonging to both sets counts on each side independently.
func TestSharedNodeCountsOnBothSides(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3"})
	if err := a.BeginJoint([]string{"3", "4", "5"}, 1); err != nil {
		t.Fatalf("BeginJoint: %v", err)
	}

	for _, node := range []string{"1", "2", "3", "4"} {
		if _, err := a.Ack(node, 9); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	// Old side majority at 9: 1,2,3. New side majority at 9: shared 3 and 4.
	if got := a.Commit(); got != 9 {
		t.Fatalf("commit = %d; want 9 with shared node 3 counted on both sides", got)
	}
}

// Even-sized sets need floor(size/2)+1: four members need three.
func TestEvenQuorum(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"a", "b", "c", "d"})

	for _, node := range []string{"a", "b"} {
		if _, err := a.Ack(node, 7); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	if got := a.Commit(); got != 0 {
		t.Fatalf("commit = %d; 2 of 4 is not a quorum", got)
	}

	if commit, err := a.Ack("c", 7); err != nil || commit != 7 {
		t.Fatalf("Ack(c,7) = %d, %v; want 7 with 3 of 4", commit, err)
	}

	if err := a.BeginJoint([]string{"c", "d", "e", "f"}, 8); err != nil {
		t.Fatalf("BeginJoint: %v", err)
	}
	// New set has 4/4 at 8, but old set has only c,d (2 of 4): blocked.
	for _, node := range []string{"c", "d", "e", "f"} {
		if _, err := a.Ack(node, 8); err != nil {
			t.Fatalf("Ack: %v", err)
		}
	}
	if got := a.Commit(); got != 7 {
		t.Fatalf("commit = %d; want 7 while old 4-set lacks its 3rd voter", got)
	}
	if _, err := a.Ack("b", 8); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	if got := a.Commit(); got != 8 {
		t.Fatalf("commit = %d; want 8 once old side reaches 3 of 4", got)
	}
}

// BeginJoint takes effect immediately: an index one ack short under the
// single config stays stuck while joint, but the existing commit is kept.
func TestBeginJointImmediateAndCommitNeverDecreases(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3"})

	// Index 4 has a majority (1,2); index 5 is one node short.
	if _, err := a.Ack("1", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ack("2", 4); err != nil {
		t.Fatal(err)
	}
	if got := a.Commit(); got != 4 {
		t.Fatalf("commit = %d; want 4 before joint", got)
	}

	if err := a.BeginJoint([]string{"3", "4", "5"}, 5); err != nil {
		t.Fatalf("BeginJoint: %v", err)
	}
	// Under joint quorum even 4 now lacks both majorities; commit must hold.
	if _, err := a.Ack("3", 5); err != nil {
		t.Fatal(err)
	}
	if got := a.Commit(); got != 4 {
		t.Fatalf("commit = %d; must remain 4 (never decrease) during joint", got)
	}

	if _, err := a.Ack("4", 4); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ack("5", 4); err != nil {
		t.Fatal(err)
	}
	if got := a.Commit(); got != 4 {
		t.Fatalf("commit = %d; want 4", got)
	}

	if err := a.FinishJoint(6); !errors.Is(err, ErrJointNotCommitted) {
		t.Fatalf("FinishJoint before joint commit = %v; want ErrJointNotCommitted", err)
	}

	// Old side already has 1,3 at 5; new side 4,5 completes both majorities.
	if _, err := a.Ack("4", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Ack("5", 5); err != nil {
		t.Fatal(err)
	}
	if got := a.Commit(); got != 5 {
		t.Fatalf("commit = %d; want 5 once both sides replicate it", got)
	}

	if err := a.FinishJoint(6); err != nil {
		t.Fatalf("FinishJoint: %v", err)
	}
	// A stale ack from removed node 1 must be rejected as unknown.
	if _, err := a.Ack("1", 10); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Ack forgotten node = %v; want ErrUnknownNode", err)
	}
}

// commit == jointIdx allows FinishJoint; commit == jointIdx-1 is rejected.
func TestFinishJointCommitBoundary(t *testing.T) {
	build := func(level uint64) *Arbiter {
		a, _ := newTestArbiter(t, []string{"1", "2", "3"})
		if err := a.BeginJoint([]string{"3", "4", "5"}, 5); err != nil {
			t.Fatal(err)
		}
		for _, node := range []string{"1", "2", "3", "4", "5"} {
			if _, err := a.Ack(node, level); err != nil {
				t.Fatal(err)
			}
		}
		return a
	}

	oneBelow := build(4)
	if got := oneBelow.Commit(); got != 4 {
		t.Fatalf("commit = %d; want 4", got)
	}
	if err := oneBelow.FinishJoint(6); !errors.Is(err, ErrJointNotCommitted) {
		t.Fatalf("FinishJoint with commit=jointIdx-1 = %v; want ErrJointNotCommitted", err)
	}

	exact := build(5)
	if got := exact.Commit(); got != 5 {
		t.Fatalf("commit = %d; want 5", got)
	}
	if err := exact.FinishJoint(6); err != nil {
		t.Fatalf("FinishJoint with commit==jointIdx: %v", err)
	}
}

// FinishJoint can jump commit forward once the stricter quorum is relaxed.
func TestCommitJumpsAfterFinishJoint(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3"})
	if err := a.BeginJoint([]string{"3", "4", "5"}, 3); err != nil {
		t.Fatal(err)
	}
	// Everyone replicates the joint entry at 3; only new-set voters reach 8.
	for _, node := range []string{"1", "2", "3", "4", "5"} {
		if _, err := a.Ack(node, 3); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []string{"3", "4", "5"} {
		if _, err := a.Ack(node, 8); err != nil {
			t.Fatal(err)
		}
	}
	if got := a.Commit(); got != 3 {
		t.Fatalf("commit = %d; want 3 while old side blocks 8", got)
	}
	if err := a.FinishJoint(4); err != nil {
		t.Fatalf("FinishJoint: %v", err)
	}
	if got := a.Commit(); got != 8 {
		t.Fatalf("commit = %d; want jump to 8 after switching to new single config", got)
	}
}

// Forgotten nodes report unknown; a node rejoining later restarts at 0.
func TestForgottenNodeAndRejoin(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3"})
	if _, err := a.Ack("1", 6); err != nil {
		t.Fatal(err)
	}
	if err := a.BeginJoint([]string{"2", "3", "4"}, 1); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"2", "3", "4"} {
		if _, err := a.Ack(node, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.FinishJoint(2); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Match("1"); ok {
		t.Fatal("node 1 should be forgotten")
	}
	if _, err := a.Ack("1", 9); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Ack forgotten node = %v; want ErrUnknownNode", err)
	}

	// Node 1 rejoins via a later joint; its match index restarts at 0.
	if err := a.BeginJoint([]string{"1", "3", "4"}, 3); err != nil {
		t.Fatal(err)
	}
	if m, ok := a.Match("1"); !ok || m != 0 {
		t.Fatalf("rejoined node match = %d,%v; want 0,true", m, ok)
	}
}

// All rejection paths leave the full observable state untouched.
func TestRejectionsDoNotChangeState(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3"})
	if _, err := a.Ack("1", 4); err != nil {
		t.Fatal(err)
	}

	snapshot := func() string {
		a.mu.Lock()
		defer a.mu.Unlock()
		return fmt.Sprintf("joint=%v cfg=%d jointIdx=%d commit=%d old=%v new=%v match=%v",
			a.joint, a.cfgIdx, a.jointIdx, a.commit, a.old, a.new, a.match)
	}
	before := snapshot()

	if _, err := a.Ack("9", 4); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Ack unknown = %v", err)
	}
	if err := a.BeginJoint(nil, 1); !errors.Is(err, ErrEmptySet) {
		t.Fatalf("empty set = %v", err)
	}
	if err := a.BeginJoint([]string{"4", "4"}, 1); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("dup set = %v", err)
	}
	if err := a.BeginJoint([]string{"4", ""}, 1); !errors.Is(err, ErrEmptyNodeID) {
		t.Fatalf("empty id = %v", err)
	}
	if err := a.BeginJoint([]string{"3", "2", "1"}, 1); !errors.Is(err, ErrSameSet) {
		t.Fatalf("same set = %v", err)
	}
	if err := a.BeginJoint([]string{"4", "5"}, 0); !errors.Is(err, ErrIndexNotAfterCfg) {
		t.Fatalf("idx<=cfgIdx = %v", err)
	}
	if err := a.FinishJoint(1); !errors.Is(err, ErrNotJoint) {
		t.Fatalf("FinishJoint in single config = %v", err)
	}
	if got := snapshot(); got != before {
		t.Fatalf("state changed after rejected ops:\nbefore %s\nafter  %s", before, got)
	}

	if err := a.BeginJoint([]string{"3", "4", "5"}, 1); err != nil {
		t.Fatal(err)
	}
	jointBefore := snapshot()
	if err := a.BeginJoint([]string{"6"}, 2); !errors.Is(err, ErrNotSingle) {
		t.Fatalf("BeginJoint while joint = %v", err)
	}
	if err := a.FinishJoint(1); !errors.Is(err, ErrIndexNotAfterJoint) {
		t.Fatalf("idx<=jointIdx = %v", err)
	}
	if err := a.FinishJoint(2); !errors.Is(err, ErrJointNotCommitted) {
		t.Fatalf("uncommitted joint = %v", err)
	}
	if got := snapshot(); got != jointBefore {
		t.Fatalf("state changed after rejected joint ops:\nbefore %s\nafter  %s", jointBefore, got)
	}

	// Ack below current match is accepted as a no-op.
	if commit, err := a.Ack("1", 2); err != nil || commit != 0 {
		t.Fatalf("stale Ack = %d,%v; want 0,nil", commit, err)
	}

	// Constructor validation.
	for _, nodes := range [][]string{nil, {}, {""}, {"a", "a"}} {
		if _, err := New(nodes); err == nil {
			t.Fatalf("New(%v) = nil error", nodes)
		}
	}
}
