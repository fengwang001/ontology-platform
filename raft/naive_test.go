package raft

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// naive is a deliberately simple reference implementation: it checks
// every index by brute force and re-derives each rejection reason in
// the mandated order, independently of Arbitrator's sorted-quorum
// shortcut.
type naive struct {
	joint    bool
	cur      []string
	next     []string
	cfgIdx   uint64
	jointIdx uint64
	commit   uint64
	match    map[string]uint64
}

func newNaive(nodes []string) *naive {
	match := make(map[string]uint64, len(nodes))
	for _, id := range nodes {
		match[id] = 0
	}
	return &naive{cur: append([]string(nil), nodes...), match: match}
}

func (n *naive) committable(idx uint64) bool {
	quorum := func(set []string) bool {
		count := 0
		for _, id := range set {
			if n.match[id] >= idx {
				count++
			}
		}
		return count >= len(set)/2+1
	}
	if !quorum(n.cur) {
		return false
	}
	return !n.joint || quorum(n.next)
}

// recompute walks index by index from 0 upward, keeping the largest
// committable one; the commit index never decreases.
func (n *naive) recompute() {
	best := n.commit
	for idx := uint64(0); ; idx++ {
		if n.committable(idx) {
			if idx > best {
				best = idx
			}
			continue
		}
		if idx > best {
			break // past the largest committable index
		}
	}
	n.commit = best
}

func (n *naive) ack(node string, idx uint64) (uint64, error) {
	if _, ok := n.match[node]; !ok {
		return 0, ErrUnknownNode
	}
	if idx > n.match[node] {
		n.match[node] = idx
	}
	n.recompute()
	return n.commit, nil
}

func (n *naive) beginJoint(newSet []string, idx uint64) error {
	if n.joint {
		return ErrNotSingleConfig
	}
	if len(newSet) == 0 {
		return ErrEmptySet
	}
	seen := map[string]bool{}
	for _, id := range newSet {
		if seen[id] {
			return ErrDuplicateNode
		}
		seen[id] = true
	}
	for _, id := range newSet {
		if id == "" {
			return ErrEmptyNodeID
		}
	}
	if len(newSet) == len(n.cur) {
		same := true
		for _, id := range newSet {
			if !contains(n.cur, id) {
				same = false
				break
			}
		}
		if same {
			return ErrSameSet
		}
	}
	if idx <= n.cfgIdx {
		return ErrIdxNotAfterCfg
	}
	n.joint = true
	n.next = append([]string(nil), newSet...)
	n.jointIdx = idx
	for _, id := range newSet {
		if _, ok := n.match[id]; !ok {
			n.match[id] = 0
		}
	}
	n.recompute()
	return nil
}

func (n *naive) finishJoint(idx uint64) error {
	if !n.joint {
		return ErrNotJointConfig
	}
	if idx <= n.jointIdx {
		return ErrIdxNotAfterJoint
	}
	if n.commit < n.jointIdx {
		return ErrCommitBelowJoint
	}
	n.joint = false
	n.cur = n.next
	n.next = nil
	n.cfgIdx = idx
	for id := range n.match {
		if !contains(n.cur, id) {
			delete(n.match, id)
		}
	}
	n.recompute()
	return nil
}

func contains(set []string, id string) bool {
	for _, member := range set {
		if member == id {
			return true
		}
	}
	return false
}

// op is one randomized operation applied to both implementations.
type op struct {
	kind   string // "ack", "begin", "finish"
	node   string
	newSet []string
	idx    uint64
}

func (o op) String() string {
	switch o.kind {
	case "ack":
		return fmt.Sprintf("Ack(%q, %d)", o.node, o.idx)
	case "begin":
		return fmt.Sprintf("BeginJoint(%v, %d)", o.newSet, o.idx)
	default:
		return fmt.Sprintf("FinishJoint(%d)", o.idx)
	}
}

func randomOps(r *rand.Rand, count int) []op {
	nodes := []string{"1", "2", "3", "4", "5", "6", "ghost", ""}
	ops := make([]op, 0, count)
	for i := 0; i < count; i++ {
		switch r.Intn(3) {
		case 0:
			ops = append(ops, op{
				kind: "ack",
				node: nodes[r.Intn(len(nodes))],
				idx:  uint64(r.Intn(12)),
			})
		case 1:
			size := r.Intn(5) // 0..4, empty sets included on purpose
			set := make([]string, size)
			for j := range set {
				set[j] = nodes[r.Intn(len(nodes)-1)] // may duplicate
			}
			ops = append(ops, op{kind: "begin", newSet: set, idx: uint64(r.Intn(12))})
		default:
			ops = append(ops, op{kind: "finish", idx: uint64(r.Intn(12))})
		}
	}
	return ops
}

func runSequence(t *testing.T, ops []op, log bool) ([]uint64, []error) {
	t.Helper()
	a := mustNew(t, "1", "2", "3")
	n := newNaive([]string{"1", "2", "3"})
	commits := make([]uint64, 0, len(ops))
	errs := make([]error, 0, len(ops))
	for i, o := range ops {
		var gotC, wantC uint64
		var gotErr, wantErr error
		switch o.kind {
		case "ack":
			gotC, gotErr = a.Ack(o.node, o.idx)
			wantC, wantErr = n.ack(o.node, o.idx)
		case "begin":
			gotErr = a.BeginJoint(o.newSet, o.idx)
			wantErr = n.beginJoint(o.newSet, o.idx)
			gotC, wantC = a.Commit(), n.commit
		default:
			gotErr = a.FinishJoint(o.idx)
			wantErr = n.finishJoint(o.idx)
			gotC, wantC = a.Commit(), n.commit
		}
		if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && gotErr != wantErr) {
			t.Fatalf("op %d %s: err = %v, naive = %v", i, o, gotErr, wantErr)
		}
		if gotC != wantC {
			t.Fatalf("op %d %s: commit = %d, naive = %d", i, o, gotC, wantC)
		}
		if log {
			t.Logf("op %d: %-28s -> commit=%d err=%v", i, o, gotC, gotErr)
		}
		commits = append(commits, gotC)
		errs = append(errs, gotErr)
	}
	return commits, errs
}

// Random operation sequences must agree with the naive per-index
// brute-force implementation on every commit and every error.
func TestRandomSequencesMatchNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		ops := randomOps(rand.New(rand.NewSource(seed)), 300)
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runSequence(t, ops, seed == 0)
		})
	}
}

// Replaying the same sequence must reproduce the exact same commit and
// error sequences.
func TestReplayDeterminism(t *testing.T) {
	ops := randomOps(rand.New(rand.NewSource(42)), 500)
	commits1, errs1 := runSequence(t, ops, false)
	commits2, errs2 := runSequence(t, ops, false)
	for i := range ops {
		if commits1[i] != commits2[i] || (errs1[i] == nil) != (errs2[i] == nil) {
			t.Fatalf("op %d %s diverged on replay: (%d,%v) vs (%d,%v)",
				i, ops[i], commits1[i], errs1[i], commits2[i], errs2[i])
		}
	}
}

// Concurrent Acks must be race-free, equivalent to some serial order,
// and the commit index must never decrease.
func TestConcurrentAcks(t *testing.T) {
	a := mustNew(t, "1", "2", "3", "4", "5")
	nodes := []string{"1", "2", "3", "4", "5"}
	const workers = 8
	const acksPerWorker = 500

	results := make(chan uint64, workers*acksPerWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			prev := uint64(0)
			for i := 0; i < acksPerWorker; i++ {
				c, err := a.Ack(nodes[r.Intn(len(nodes))], uint64(r.Intn(1000)))
				if err != nil {
					t.Errorf("Ack: %v", err)
					return
				}
				if c < prev {
					t.Errorf("commit decreased: %d -> %d", prev, c)
					return
				}
				prev = c
				results <- c
			}
		}(int64(w))
	}
	wg.Wait()
	close(results)

	final := a.Commit()
	maxSeen := uint64(0)
	for c := range results {
		if c > final {
			t.Fatalf("observed commit %d above final %d", c, final)
		}
		if c > maxSeen {
			maxSeen = c
		}
	}
	if maxSeen != final {
		t.Fatalf("last serial op should return final commit: max=%d final=%d", maxSeen, final)
	}
}

// Sorted shortcut vs brute force on identical random match tables.
func TestQuorumIndexMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for trial := 0; trial < 200; trial++ {
		size := 1 + r.Intn(6)
		set := map[string]struct{}{}
		match := map[string]uint64{}
		for i := 0; i < size; i++ {
			id := fmt.Sprintf("n%d", i)
			set[id] = struct{}{}
			match[id] = uint64(r.Intn(20))
		}
		got := quorumIndex(set, match)
		want := uint64(0)
		for idx := uint64(0); ; idx++ {
			count := 0
			for id := range set {
				if match[id] >= idx {
					count++
				}
			}
			if count < len(set)/2+1 {
				break
			}
			want = idx
		}
		if got != want {
			t.Fatalf("trial %d: quorumIndex = %d, brute force = %d (match=%v)",
				trial, got, want, match)
		}
	}
}

// The joint candidate is the minimum of the two per-set candidates,
// not any quorum over the union.
func TestJointCandidateIsMinOfSets(t *testing.T) {
	a := mustNew(t, "1", "2", "3")
	mustBeginJoint(t, a, []string{"3", "4", "5"}, 1)
	mustAck(t, a, "1", 8)
	mustAck(t, a, "2", 8)
	mustAck(t, a, "3", 8)
	// Union {1..5} has 3 of 5 at 8, but new set {3,4,5} has only one.
	if c := a.Commit(); c != 0 {
		t.Fatalf("commit = %d, want 0", c)
	}
	// Acking only new-set members cannot commit either: old set needs
	// its own quorum, which it has; both sides are required.
	got := []uint64{}
	got = append(got, mustAck(t, a, "4", 8))
	got = append(got, mustAck(t, a, "5", 8))
	want := []uint64{8, 8}
	if !equalUint64s(got, want) {
		t.Fatalf("commit sequence = %v, want %v", got, want)
	}
}

func equalUint64s(a, b []uint64) bool {
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
