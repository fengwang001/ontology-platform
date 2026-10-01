package raft

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, nodes ...string) *Arbitrator {
	t.Helper()
	a, err := NewArbitrator(nodes)
	if err != nil {
		t.Fatalf("NewArbitrator(%v): %v", nodes, err)
	}
	return a
}

func mustAck(t *testing.T, a *Arbitrator, node string, idx uint64) uint64 {
	t.Helper()
	c, err := a.Ack(node, idx)
	if err != nil {
		t.Fatalf("Ack(%q, %d): %v", node, idx, err)
	}
	return c
}

func mustBeginJoint(t *testing.T, a *Arbitrator, newSet []string, idx uint64) {
	t.Helper()
	if err := a.BeginJoint(newSet, idx); err != nil {
		t.Fatalf("BeginJoint(%v, %d): %v", newSet, idx, err)
	}
}

func mustFinishJoint(t *testing.T, a *Arbitrator, idx uint64) {
	t.Helper()
	if err := a.FinishJoint(idx); err != nil {
		t.Fatalf("FinishJoint(%d): %v", idx, err)
	}
}

func TestConstructorValidation(t *testing.T) {
	cases := []struct {
		name  string
		nodes []string
		want  error
	}{
		{"empty set", nil, ErrEmptySet},
		{"duplicate", []string{"1", "2", "1"}, ErrDuplicateNode},
		{"empty id", []string{"1", ""}, ErrEmptyNodeID},
	}
	for _, tc := range cases {
		if _, err := NewArbitrator(tc.nodes); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	a := mustNew(t, "1", "2", "3")
	if a.cfgIdx != 0 || a.commit != 0 {
		t.Fatalf("initial cfgIdx=%d commit=%d, want 0/0", a.cfgIdx, a.commit)
	}
}

// Joint (old={1,2,3}, new={3,4,5}) with match 1,2,3 = 5 and 4,5 = 0
// must not commit: a majority of the union (3 of 5) would wrongly pass.
func TestJointQuorumIsNotUnionMajority(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 1)
	for _, n := range []string{"1", "2", "3"} {
		mustAck(t, a, n, 5)
	}
	if c := a.Commit(); c != 0 {
		t.Fatalf("joint commit = %d, want 0 (union majority would say 5)", c)
	}
	if c := mustAck(t, a, "4", 5); c != 5 {
		t.Fatalf("after new-set quorum, commit = %d, want 5", c)
	}
}

// A node in both sets casts one vote in each set's quorum.
func TestSharedNodeCountsOnBothSides(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 1)
	mustAck(t, a, "1", 5)
	mustAck(t, a, "3", 5) // old: {1,3} quorum met; new: only {3}
	if c := a.Commit(); c != 0 {
		t.Fatalf("commit = %d, want 0: node 3 alone is not a new-set quorum", c)
	}
	if c := mustAck(t, a, "4", 5); c != 5 {
		t.Fatalf("commit = %d, want 5: node 3 counted on both sides", c)
	}
}

// An even-size set of 4 needs 3 matching members.
func TestEvenSetQuorum(t *testing.T) {
	a := mustNew(t, "a", "b", "c", "d")
	mustAck(t, a, "a", 7)
	mustAck(t, a, "b", 7)
	if c := a.Commit(); c != 0 {
		t.Fatalf("commit = %d with 2 of 4, want 0", c)
	}
	if c := mustAck(t, a, "c", 7); c != 7 {
		t.Fatalf("commit = %d with 3 of 4, want 7", c)
	}
}

// BeginJoint takes effect immediately: an index one Ack short of
// committing under the single config stops advancing once joint, while
// the already-committed index never regresses.
func TestBeginJointImmediateEffect(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustAck(t, a, "1", 3)
	if c := mustAck(t, a, "2", 3); c != 3 {
		t.Fatalf("single-config commit = %d, want 3", c)
	}
	mustAck(t, a, "1", 9) // one short of committing 9 under {1,2,3}
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 4)
	if c := mustAck(t, a, "2", 9); c != 3 {
		t.Fatalf("joint commit = %d, want 3: 9 lacks a new-set quorum", c)
	}
	if c := mustAck(t, a, "3", 9); c != 3 {
		t.Fatalf("joint commit = %d, want 3: new set still lacks quorum", c)
	}
	if c := mustAck(t, a, "4", 9); c != 9 {
		t.Fatalf("joint commit = %d, want 9 once both sets have quorum", c)
	}
}

// After FinishJoint the commit index can jump to what the new single
// configuration alone commits.
func TestFinishJointCommitJumps(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustAck(t, a, "1", 2)
	mustAck(t, a, "2", 2)
	mustAck(t, a, "3", 10)
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 2)
	mustAck(t, a, "4", 10)
	mustAck(t, a, "5", 10)
	// Joint commit is limited by the old set: quorum of {2,2,10} is 2.
	if c := a.Commit(); c != 2 {
		t.Fatalf("joint commit = %d, want 2", c)
	}
	mustFinishJoint(t, a, 3)
	if c := a.Commit(); c != 10 {
		t.Fatalf("post-finish commit = %d, want 10", c)
	}
	if a.cfgIdx != 3 {
		t.Fatalf("cfgIdx = %d, want 3", a.cfgIdx)
	}
}

// FinishJoint requires commit >= jointIdx; equality is allowed, one
// below is rejected.
func TestFinishJointCommitBoundary(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustAck(t, a, "1", 1)
	mustAck(t, a, "2", 1)
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 2)
	// commit is 1 = jointIdx-1.
	if err := a.FinishJoint(3); !errors.Is(err, ErrCommitBelowJoint) {
		t.Fatalf("FinishJoint with commit<jointIdx: got %v, want %v", err, ErrCommitBelowJoint)
	}
	for _, n := range []string{"1", "2", "3", "4"} {
		mustAck(t, a, n, 2)
	}
	// commit is now 2 = jointIdx.
	mustFinishJoint(t, a, 3)
}

// Forgotten nodes are unknown to Ack; if they rejoin later their match
// restarts at 0.
func TestForgottenNodeUnknownAndRejoinResets(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustAck(t, a, "1", 50)
	mustBeginJoint(t, a, []string{"2", "3", "4"}, 1)
	mustAck(t, a, "2", 1)
	mustAck(t, a, "3", 1)
	mustAck(t, a, "4", 1)
	mustFinishJoint(t, a, 2) // node 1 is forgotten
	if _, err := a.Ack("1", 60); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("Ack on forgotten node: got %v, want %v", err, ErrUnknownNode)
	}
	// Node 1 rejoins with match 0, so its stale 50 must not count:
	// {1,4} needs both members, and the old set alone could reach 50.
	mustBeginJoint(t, a, []string{"1", "4"}, 3)
	for _, n := range []string{"2", "3", "4"} {
		mustAck(t, a, n, 50)
	}
	if c := a.Commit(); c != 1 {
		t.Fatalf("commit = %d, want 1: rejoined node 1 must restart at 0", c)
	}
	if c := mustAck(t, a, "1", 50); c != 50 {
		t.Fatalf("commit = %d, want 50 after node 1 catches up", c)
	}
}

func TestAckUnknownNode(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	if _, err := a.Ack("9", 1); !errors.Is(err, ErrUnknownNode) {
		t.Fatalf("got %v, want %v", err, ErrUnknownNode)
	}
	// A lower idx is not an error and keeps the maximum.
	mustAck(t, a, "1", 5)
	if c := mustAck(t, a, "1", 2); c != 0 {
		t.Fatalf("commit = %d, want 0", c)
	}
	if a.match["1"] != 5 {
		t.Fatalf("match[1] = %d, want 5", a.match["1"])
	}
}

func TestBeginJointErrorPriority(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 1)
	// Not single config beats every other problem.
	if err := a.BeginJoint(nil, 0); !errors.Is(err, ErrNotSingleConfig) {
		t.Fatalf("got %v, want %v", err, ErrNotSingleConfig)
	}
	for _, n := range []string{"1", "2", "3", "4"} {
		mustAck(t, a, n, 1)
	}
	mustFinishJoint(t, a, 2)

	cases := []struct {
		name   string
		newSet []string
		idx    uint64
		want   error
	}{
		{"empty set", nil, 3, ErrEmptySet},
		{"duplicate beats empty id", []string{"6", "6", ""}, 3, ErrDuplicateNode},
		{"empty id", []string{"6", ""}, 3, ErrEmptyNodeID},
		{"same set beats bad idx", []string{"3", "4", "5"}, 0, ErrSameSet},
		{"idx not after cfgIdx", []string{"4", "5", "6"}, 2, ErrIdxNotAfterCfg},
	}
	for _, tc := range cases {
		if err := a.BeginJoint(tc.newSet, tc.idx); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestFinishJointErrorPriority(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	// Not joint beats a bad idx.
	if err := a.FinishJoint(0); !errors.Is(err, ErrNotJointConfig) {
		t.Fatalf("got %v, want %v", err, ErrNotJointConfig)
	}
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 2)
	// idx <= jointIdx beats commit < jointIdx.
	if err := a.FinishJoint(2); !errors.Is(err, ErrIdxNotAfterJoint) {
		t.Fatalf("got %v, want %v", err, ErrIdxNotAfterJoint)
	}
	if err := a.FinishJoint(3); !errors.Is(err, ErrCommitBelowJoint) {
		t.Fatalf("got %v, want %v", err, ErrCommitBelowJoint)
	}
}

// Rejected operations must not change any state.
func TestRejectedOpsLeaveStateUntouched(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustAck(t, a, "1", 4)
	mustAck(t, a, "2", 4)
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 5)
	before := snapshot(a)

	rejections := []func() error{
		func() error { _, err := a.Ack("ghost", 9); return err },
		func() error { return a.BeginJoint([]string{"6"}, 9) },
		func() error { return a.FinishJoint(5) },
		func() error { return a.FinishJoint(6) }, // commit 4 < jointIdx 5
	}
	for i, op := range rejections {
		if err := op(); err == nil {
			t.Fatalf("rejection %d: expected error", i)
		}
		if after := snapshot(a); !reflect.DeepEqual(before, after) {
			t.Fatalf("rejection %d changed state: before=%+v after=%+v", i, before, after)
		}
	}
}

type state struct {
	joint    bool
	cur      map[string]struct{}
	next     map[string]struct{}
	cfgIdx   uint64
	jointIdx uint64
	commit   uint64
	match    map[string]uint64
}

func snapshot(a *Arbitrator) state {
	a.mu.Lock()
	defer a.mu.Unlock()
	clone := func(m map[string]struct{}) map[string]struct{} {
		if m == nil {
			return nil
		}
		out := make(map[string]struct{}, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	match := make(map[string]uint64, len(a.match))
	for k, v := range a.match {
		match[k] = v
	}
	return state{
		joint:    a.joint,
		cur:      clone(a.cur),
		next:     clone(a.next),
		cfgIdx:   a.cfgIdx,
		jointIdx: a.jointIdx,
		commit:   a.commit,
		match:    match,
	}
}
