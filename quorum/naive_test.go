package quorum

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
)

type discardLogger struct{}

func (discardLogger) Printf(string, ...any) {}

// Replaying the identical serial operation sequence yields identical commit
// indices and errors.
func TestDeterministicReplay(t *testing.T) {
	ops := []refOp{
		{kind: opAck, node: "a", idx: 2},
		{kind: opAck, node: "b", idx: 2},
		{kind: opBegin, set: []string{"c", "d"}, idx: 1},
		{kind: opFinish, idx: 1},
		{kind: opAck, node: "c", idx: 2},
		{kind: opAck, node: "d", idx: 2},
		{kind: opFinish, idx: 2},
		{kind: opAck, node: "a", idx: 9},
		{kind: opBegin, set: []string{"a", "e"}, idx: 2},
	}

	run := func() []string {
		a, err := New([]string{"a", "b", "c"})
		if err != nil {
			t.Fatal(err)
		}
		a.SetLogger(discardLogger{})
		var events []string
		for _, op := range ops {
			switch op.kind {
			case opAck:
				commit, ackErr := a.Ack(op.node, op.idx)
				events = append(events, fmt.Sprintf("ack:%d:%v", commit, ackErr))
			case opBegin:
				events = append(events, "begin:"+errKey(a.BeginJoint(op.set, op.idx)))
			case opFinish:
				events = append(events, "finish:"+errKey(a.FinishJoint(op.idx)))
			}
		}
		return events
	}

	first := run()
	second := run()
	if !slices.Equal(first, second) {
		t.Fatalf("replays differ:\n%v\n%v", first, second)
	}
}

func errKey(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

// naiveArbiter is a deliberately simple reference implementation: every
// committability decision re-checks each index one by one, counting voters
// with match >= N separately for old and new sets.
type naiveArbiter struct {
	joint            bool
	old, newv        []string
	match            map[string]uint64
	cfgIdx, jointIdx uint64
	commit           uint64
}

func newNaive(nodes []string) *naiveArbiter {
	m := map[string]uint64{}
	for _, n := range nodes {
		m[n] = 0
	}
	return &naiveArbiter{old: slices.Clone(nodes), match: m}
}

func naiveMajority(n int) int { return n/2 + 1 }

func (n *naiveArbiter) committable(idx uint64) bool {
	count := func(set []string) int {
		c := 0
		for _, node := range set {
			if n.match[node] >= idx {
				c++
			}
		}
		return c
	}
	if count(n.old) < naiveMajority(len(n.old)) {
		return false
	}
	if n.joint && count(n.newv) < naiveMajority(len(n.newv)) {
		return false
	}
	return true
}

func (n *naiveArbiter) recompute() uint64 {
	var maxMatch uint64
	for _, idx := range n.match {
		if idx > maxMatch {
			maxMatch = idx
		}
	}
	// Brute force: inspect every index individually from 1 upward.
	for idx := uint64(1); idx <= maxMatch; idx++ {
		if n.committable(idx) && idx > n.commit {
			n.commit = idx
		}
	}
	return n.commit
}

func (n *naiveArbiter) ack(node string, idx uint64) (uint64, error) {
	prev, ok := n.match[node]
	if !ok {
		return n.commit, ErrUnknownNode
	}
	if idx > prev {
		n.match[node] = idx
	}
	return n.recompute(), nil
}

func validSet(set []string) error {
	seen := map[string]bool{}
	for _, node := range set {
		if node == "" {
			return ErrEmptyNodeID
		}
		if seen[node] {
			return ErrDuplicateNode
		}
		seen[node] = true
	}
	return nil
}

func (n *naiveArbiter) beginJoint(set []string, idx uint64) error {
	switch {
	case n.joint:
		return ErrNotSingle
	case len(set) == 0:
		return ErrEmptySet
	}
	if err := validSet(set); err != nil {
		return err
	}
	if len(set) == len(n.old) {
		x, y := slices.Clone(n.old), slices.Clone(set)
		slices.Sort(x)
		slices.Sort(y)
		if slices.Equal(x, y) {
			return ErrSameSet
		}
	}
	if idx <= n.cfgIdx {
		return ErrIndexNotAfterCfg
	}
	n.joint = true
	n.jointIdx = idx
	n.newv = slices.Clone(set)
	for _, node := range set {
		if _, ok := n.match[node]; !ok {
			n.match[node] = 0
		}
	}
	n.recompute()
	return nil
}

func (n *naiveArbiter) finishJoint(idx uint64) error {
	switch {
	case !n.joint:
		return ErrNotJoint
	case idx <= n.jointIdx:
		return ErrIndexNotAfterJoint
	case n.commit < n.jointIdx:
		return ErrJointNotCommitted
	}
	set := n.newv
	for node := range n.match {
		if !slices.Contains(set, node) {
			delete(n.match, node)
		}
	}
	n.joint = false
	n.old = set
	n.newv = nil
	n.cfgIdx = idx
	n.recompute()
	return nil
}

type opKind int

const (
	opAck opKind = iota
	opBegin
	opFinish
)

type refOp struct {
	kind opKind
	node string
	set  []string
	idx  uint64
}

func TestRandomDifferential(t *testing.T) {
	universe := []string{"a", "b", "c", "d", "e"}
	rng := rand.New(rand.NewPCG(42, 99))

	for trial := 0; trial < 200; trial++ {
		initial := slices.Clone(universe[:2+rng.IntN(3)]) // 2..4 initial nodes
		real, err := New(initial)
		if err != nil {
			t.Fatal(err)
		}
		real.SetLogger(discardLogger{})
		ref := newNaive(initial)

		var trace strings.Builder
		checkErr := func(step int, got, want error) {
			t.Helper()
			if (got == nil) != (want == nil) ||
				got != nil && !errors.Is(got, want) {
				t.Fatalf("trial %d step %d error mismatch: got %v want %v\nops:\n%s",
					trial, step, got, want, trace.String())
			}
		}

		for step := 0; step < 60; step++ {
			op := refOp{kind: opKind(rng.IntN(3)), idx: uint64(rng.IntN(8))}
			switch op.kind {
			case opAck:
				op.node = universe[rng.IntN(len(universe))]
			case opBegin:
				size := 1 + rng.IntN(4)
				perm := rng.Perm(len(universe))[:size]
				for _, p := range perm {
					op.set = append(op.set, universe[p])
				}
				if rng.IntN(3) == 0 {
					op.idx = 0 // exercise idx <= cfgIdx rejections
				}
			}
			fmtOp(&trace, op)

			switch op.kind {
			case opAck:
				gotCommit, gotErr := real.Ack(op.node, op.idx)
				wantCommit, wantErr := ref.ack(op.node, op.idx)
				checkErr(step, gotErr, wantErr)
				if gotCommit != wantCommit || gotCommit != real.Commit() {
					t.Fatalf("trial %d step %d commit mismatch: got %d want %d\nops:\n%s",
						trial, step, gotCommit, wantCommit, trace.String())
				}
			case opBegin:
				gotErr := real.BeginJoint(op.set, op.idx)
				wantErr := ref.beginJoint(op.set, op.idx)
				checkErr(step, gotErr, wantErr)
			case opFinish:
				gotErr := real.FinishJoint(op.idx)
				wantErr := ref.finishJoint(op.idx)
				checkErr(step, gotErr, wantErr)
			}
		}
	}
}

func fmtOp(b *strings.Builder, op refOp) {
	switch op.kind {
	case opAck:
		b.WriteString("Ack(" + op.node + "," + uitoa(op.idx) + ")\n")
	case opBegin:
		b.WriteString("BeginJoint(" + strings.Join(op.set, ",") + "," + uitoa(op.idx) + ")\n")
	case opFinish:
		b.WriteString("FinishJoint(" + uitoa(op.idx) + ")\n")
	}
}

func uitoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

// Concurrent acks must be serializable: after every voter acks the same
// index, commit reaches exactly that index, and it never decreases.
func TestConcurrentAcks(t *testing.T) {
	a, _ := newTestArbiter(t, []string{"1", "2", "3", "4", "5"})
	var wg sync.WaitGroup
	for round := uint64(1); round <= 10; round++ {
		last := a.Commit()
		var stop sync.WaitGroup
		for _, node := range []string{"1", "2", "3", "4", "5"} {
			stop.Add(1)
			wg.Add(1)
			go func(node string, level uint64) {
				defer wg.Done()
				defer stop.Done()
				commit, err := a.Ack(node, level)
				if err != nil {
					t.Errorf("Ack: %v", err)
				}
				if commit < last {
					t.Errorf("commit decreased: %d < %d", commit, last)
				}
			}(node, round)
		}
		stop.Wait()
		if got := a.Commit(); got != round {
			t.Fatalf("after round %d commit = %d; want %d", round, got, round)
		}
	}
	wg.Wait()
}

// The logger records inputs, outputs and the quorum rationale.
func TestLoggingRecordsInputOutputAndRationale(t *testing.T) {
	a, l := newTestArbiter(t, []string{"1", "2", "3"})
	if _, err := a.Ack("1", 3); err != nil {
		t.Fatal(err)
	}
	out := l.buf.String()
	if !strings.Contains(out, `Ack("1",3)`) ||
		!strings.Contains(out, "commit 0->0") ||
		!strings.Contains(out, "majority of the active set") {
		t.Fatalf("log missing input/output/rationale:\n%s", out)
	}

	if _, err := a.Ack("zzz", 1); !errors.Is(err, ErrUnknownNode) {
		t.Fatal(err)
	}
	if !strings.Contains(l.buf.String(), "unknown node") {
		t.Fatalf("log missing rejection reason:\n%s", l.buf.String())
	}
}
