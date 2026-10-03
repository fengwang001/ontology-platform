package ontology

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"sync"
	"testing"
)

type nodeInfo struct {
	parent int
	name   string
}

func TestNewReplayerValidation(t *testing.T) {
	cases := [][]string{
		nil,
		{},
		{""},
		{"A", "A"},
	}
	for _, replicas := range cases {
		if _, err := NewReplayer(replicas); err == nil {
			t.Fatalf("NewReplayer(%v) error=nil", replicas)
		}
	}
}

func TestSameArrivalSequenceReturnsIdenticalResults(t *testing.T) {
	ops := []Op{
		{TS: 3, Rep: "B", Node: 2, Parent: 3, Name: "b"},
		{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "a"},
		{TS: 2, Rep: "A", Node: 3, Parent: 0, Name: "c"},
	}
	results := make([][]ApplyResult, 2)
	for run := range results {
		r := mustReplayer(t, "A", "B")
		for _, op := range ops {
			result, err := r.Apply(op)
			if err != nil {
				t.Fatal(err)
			}
			results[run] = append(results[run], result)
		}
	}
	if !reflect.DeepEqual(results[0], results[1]) {
		t.Fatalf("results differ: %+v vs %+v", results[0], results[1])
	}
}

func TestCanonicalMoveOrderings(t *testing.T) {
	ops := []Op{
		{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "x"},
		{TS: 2, Rep: "A", Node: 3, Parent: 0, Name: "y"},
		{TS: 3, Rep: "A", Node: 2, Parent: 3, Name: "x"},
		{TS: 3, Rep: "B", Node: 3, Parent: 2, Name: "y"},
	}

	t.Run("natural order", func(t *testing.T) {
		r := mustReplayer(t, "A", "B")
		for _, op := range ops {
			result, err := r.Apply(op)
			if err != nil {
				t.Fatalf("Apply(%+v): %v", op, err)
			}
			t.Logf("input=%+v output_effective=%v output_changed=%+v reason=%s", op, result.Effective, result.Changed, decisionBasis(r, op))
		}
		assertTree(t, r, map[int]nodeInfo{2: {3, "x"}, 3: {0, "y"}})
		assertLog(t, r, map[Key]bool{{1, "A"}: true, {2, "A"}: true, {3, "A"}: true, {3, "B"}: false})
	})

	t.Run("reverse concurrent tie arrival", func(t *testing.T) {
		r := mustReplayer(t, "A", "B")
		for _, idx := range []int{0, 1, 3, 2} {
			op := ops[idx]
			beforeRedone := r.redone
			result, err := r.Apply(op)
			if err != nil {
				t.Fatalf("Apply(%+v): %v", op, err)
			}
			t.Logf("input=%+v output_effective=%v output_changed=%+v redone=%d reason=%s", op, result.Effective, result.Changed, r.redone-beforeRedone, decisionBasis(r, op))
			if idx == 2 {
				if !result.Effective || !reflect.DeepEqual(result.Changed, []Key{{3, "B"}}) || r.redone-beforeRedone != 1 {
					t.Fatalf("last result=%+v redone=%d", result, r.redone-beforeRedone)
				}
			}
		}
		assertTree(t, r, map[int]nodeInfo{2: {3, "x"}, 3: {0, "y"}})
		assertLog(t, r, map[Key]bool{{1, "A"}: true, {2, "A"}: true, {3, "A"}: true, {3, "B"}: false})
	})
}

func TestSkipParentEqualsNode(t *testing.T) {
	r := mustReplayer(t, "A")
	op := Op{TS: 1, Rep: "A", Node: 2, Parent: 2, Name: "self"}
	result, err := r.Apply(op)
	if err != nil || result.Effective {
		t.Fatalf("Apply self-parent=(%+v,%v), want accepted and skipped", result, err)
	}
	t.Logf("input=%+v output_effective=%v reason=%s", op, result.Effective, decisionBasis(r, op))
	if _, _, exists := r.Parent(2); exists {
		t.Fatal("skipped self-parent operation created node 2")
	}
	if entries := r.Log(); len(entries) != 1 || entries[0].Effective {
		t.Fatalf("log=%+v, want one skipped entry", entries)
	}
}

func TestLateParentActivatesSkippedChild(t *testing.T) {
	r := mustReplayer(t, "A")
	child := Op{TS: 2, Rep: "A", Node: 3, Parent: 2, Name: "child"}
	parent := Op{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "parent"}

	first, err := r.Apply(child)
	if err != nil || first.Effective {
		t.Fatalf("child before parent=(%+v,%v)", first, err)
	}
	before := r.redone
	second, err := r.Apply(parent)
	if err != nil || !second.Effective || !reflect.DeepEqual(second.Changed, []Key{{2, "A"}}) {
		t.Fatalf("late parent=(%+v,%v)", second, err)
	}
	t.Logf("input=%+v output_effective=%v changed=%+v redone=%d reason=%s", parent, second.Effective, second.Changed, r.redone-before, "absent parent is created by lower-key operation, then suffix is replayed")
	assertTree(t, r, map[int]nodeInfo{2: {0, "parent"}, 3: {2, "child"}})
	if r.redone-before != 1 {
		t.Fatalf("redone delta=%d, want 1", r.redone-before)
	}
}

func TestTrashMoveOutAndConcurrentMove(t *testing.T) {
	r := mustReplayer(t, "A", "B")
	ops := []Op{
		{TS: 1, Rep: "A", Node: 2, Parent: 1, Name: "trashed"},
		{TS: 2, Rep: "A", Node: 2, Parent: 0, Name: "restored"},
		{TS: 3, Rep: "A", Node: 3, Parent: 2, Name: "a-name"},
		{TS: 4, Rep: "B", Node: 3, Parent: 2, Name: "b-name"},
		{TS: 5, Rep: "A", Node: 3, Parent: 1, Name: "trashed-child"},
	}
	for _, op := range ops {
		result, err := r.Apply(op)
		if err != nil || !result.Effective {
			t.Fatalf("Apply(%+v)=(%+v,%v)", op, result, err)
		}
		t.Logf("input=%+v output_effective=%v in_trash_node2=%v in_trash_node3=%v", op, result.Effective, r.InTrash(2), r.InTrash(3))
	}
	assertTree(t, r, map[int]nodeInfo{
		2: {0, "restored"},
		3: {1, "trashed-child"},
	})
	if r.InTrash(2) {
		t.Fatal("node 2 moved back from trash but InTrash=true")
	}
	if !r.InTrash(3) {
		t.Fatal("node 3 moved to trash but InTrash=false")
	}
}

func TestConcurrentMoveLargerKeyWins(t *testing.T) {
	ops := []Op{
		{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "anchor"},
		{TS: 2, Rep: "A", Node: 3, Parent: 2, Name: "created"},
		{TS: 3, Rep: "A", Node: 3, Parent: 0, Name: "a-wins?"},
		{TS: 3, Rep: "B", Node: 3, Parent: 2, Name: "b-wins"},
	}
	for _, order := range [][]int{{0, 1, 2, 3}, {0, 1, 3, 2}} {
		t.Run("", func(t *testing.T) {
			r := mustReplayer(t, "A", "B")
			for _, idx := range order {
				result, err := r.Apply(ops[idx])
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("arrival=%d input=%+v output_effective=%v", idx, ops[idx], result.Effective)
			}
			assertTree(t, r, map[int]nodeInfo{2: {0, "anchor"}, 3: {2, "b-wins"}})
		})
	}
}

func TestStableBoundaryFoldingAndChanged(t *testing.T) {
	r := mustReplayer(t, "A", "B")
	ops := []Op{
		{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "first"},
		{TS: 2, Rep: "A", Node: 3, Parent: 0, Name: "second"},
	}
	for _, op := range ops {
		if _, err := r.Apply(op); err != nil {
			t.Fatal(err)
		}
	}
	if folded, err := r.Ack("A", 2); err != nil || folded != 0 {
		t.Fatalf("Ack A=(%d,%v), want 0 because other replica is at 0", folded, err)
	}
	folded, err := r.Ack("B", 2)
	if err != nil || folded != 2 {
		t.Fatalf("Ack B=(%d,%v), want 2 folded", folded, err)
	}
	t.Logf("input_ack=B:2 output_folded=%d output_log_len=%d", folded, len(r.Log()))
	if r.Stable() != 2 || len(r.Log()) != 0 {
		t.Fatalf("stable=%d log=%+v", r.Stable(), r.Log())
	}

	if _, err := r.Apply(Op{TS: 2, Rep: "A", Node: 4, Parent: 0, Name: "exact"}); !errors.Is(err, ErrStale) {
		t.Fatalf("ts==stable error=%v, want ErrStale", err)
	}
	live := Op{TS: 3, Rep: "A", Node: 4, Parent: 2, Name: "live"}
	result, err := r.Apply(live)
	if err != nil || !result.Effective || len(result.Changed) != 0 {
		t.Fatalf("stable+1 Apply=(%+v,%v), want effective with empty Changed", result, err)
	}
	late := Op{TS: 4, Rep: "B", Node: 2, Parent: 4, Name: "would-cycle"}
	result, err = r.Apply(late)
	if err != nil || result.Effective || len(result.Changed) != 0 {
		t.Fatalf("late cycle=(%+v,%v), folded operations must not appear in Changed", result, err)
	}
	t.Logf("input=%+v output_effective=%v changed=%+v reason=%s", late, result.Effective, result.Changed, decisionBasis(r, late))
	assertTree(t, r, map[int]nodeInfo{2: {0, "first"}, 3: {0, "second"}, 4: {2, "live"}})
}

func TestAckPriorityAndRegression(t *testing.T) {
	r := mustReplayer(t, "A")
	if _, err := r.Ack("ghost", -1); !errors.Is(err, ErrUnknownRep) {
		t.Fatalf("unknown regression Ack error=%v, want ErrUnknownRep", err)
	}
	if _, err := r.Ack("A", 3); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Ack("A", 2); !errors.Is(err, ErrAckRegress) {
		t.Fatalf("regress error=%v, want ErrAckRegress", err)
	}
	if r.Stable() != 3 {
		t.Fatalf("stable after failed regression=%d, want 3", r.Stable())
	}
}

func TestApplyRejectionPriority(t *testing.T) {
	r := mustReplayer(t, "A")
	valid := Op{TS: 1, Rep: "A", Node: 2, Parent: 0, Name: "x"}
	if _, err := r.Apply(valid); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(valid); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate before stable error=%v, want ErrDuplicate", err)
	}
	cases := []struct {
		name string
		op   Op
		want error
	}{
		{"invalid before unknown replica", Op{TS: 0, Rep: "ghost", Node: 2, Parent: 0, Name: "x"}, nil},
		{"unknown before stale", Op{TS: 1, Rep: "ghost", Node: 2, Parent: 0, Name: "x"}, ErrUnknownRep},
		{"stale before duplicate", valid, ErrStale},
	}
	if _, err := r.Ack("A", 1); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Apply(tc.op)
			if tc.want == nil {
				if err == nil || err.Error() != "invalid operation" {
					t.Fatalf("error=%v, want invalid operation", err)
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v, want %v", err, tc.want)
			}
		})
	}
}

func TestConcurrentAccess(t *testing.T) {
	r := mustReplayer(t, "A", "B")
	var wg sync.WaitGroup
	for i := int64(1); i <= 80; i++ {
		rep := "A"
		if i%2 == 0 {
			rep = "B"
		}
		wg.Add(1)
		go func(i int64, rep string) {
			defer wg.Done()
			_, _ = r.Apply(Op{TS: i, Rep: rep, Node: int(2 + i%6), Parent: 0, Name: "n"})
			_, _, _ = r.Parent(int(2 + i%6))
			_ = r.InTrash(int(2 + i%6))
			_ = r.Log()
			_, _ = r.Ack(rep, 0)
		}(i, rep)
	}
	wg.Wait()
}

func TestRandomOrdersMatchNaiveReplay(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewPCG(uint64(seed), uint64(seed*7+1)))
		ops := randomOps(rng)
		order1 := rng.Perm(len(ops))
		order2 := rng.Perm(len(ops))

		r1 := mustReplayer(t, "A", "B", "C")
		r2 := mustReplayer(t, "A", "B", "C")
		applyInRandomOrder(t, r1, ops, order1)
		applyInRandomOrder(t, r2, ops, order2)

		naiveTree, naiveLog := replayNaive(ops)
		tree1 := snapshotTree(r1)
		tree2 := snapshotTree(r2)
		log1 := r1.Log()
		log2 := r2.Log()

		if !reflect.DeepEqual(tree1, tree2) || !reflect.DeepEqual(tree1, naiveTree) {
			t.Logf("seed=%d input_ops=%+v order1=%v order2=%v", seed, ops, order1, order2)
			t.Logf("actual_tree=%+v naive_tree=%+v decision=naive_total_order_replay", tree1, naiveTree)
			t.Fatalf("seed=%d trees differ:\nactual=%+v\nnaive=%+v", seed, tree1, naiveTree)
		}
		if !reflect.DeepEqual(log1, log2) || !reflect.DeepEqual(log1, naiveLog) {
			t.Logf("seed=%d input_ops=%+v order1=%v order2=%v", seed, ops, order1, order2)
			t.Logf("actual_log=%+v naive_log=%+v decision=naive_total_order_replay", log1, naiveLog)
			t.Fatalf("seed=%d logs differ:\nactual=%+v\nnaive=%+v", seed, log1, naiveLog)
		}
		assertParentStepBound(t, r1)
	}
	t.Log("input=2000 deterministic random operation sets output=both arrival orders matched naive total-order replay decision=skip-when-parent-missing-or-parent-chain-reaches-node")
}

func mustReplayer(t *testing.T, replicas ...string) *Replayer {
	t.Helper()
	r, err := NewReplayer(replicas)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func assertTree(t *testing.T, r *Replayer, want map[int]nodeInfo) {
	t.Helper()
	got := snapshotTree(r)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tree = %+v, want %+v", got, want)
	}
}

func assertLog(t *testing.T, r *Replayer, want map[Key]bool) {
	t.Helper()
	entries := r.Log()
	got := make(map[Key]bool, len(entries))
	for _, entry := range entries {
		got[entry.Key] = entry.Effective
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("log = %+v, want %+v", entries, want)
	}
}

func snapshotTree(r *Replayer) map[int]nodeInfo {
	result := make(map[int]nodeInfo)
	for node := range r.parent {
		if node <= 1 {
			continue
		}
		result[node] = nodeInfo{parent: r.parent[node], name: r.name[node]}
	}
	return result
}

func randomOps(rng *rand.Rand) []Op {
	seen := make(map[Key]bool)
	ops := make([]Op, 0, 20)
	for len(ops) < 20 {
		key := Key{
			TS:  int64(rng.IntN(8) + 1),
			Rep: []string{"A", "B", "C"}[rng.IntN(3)],
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		ops = append(ops, Op{
			TS:     key.TS,
			Rep:    key.Rep,
			Node:   rng.IntN(7) + 2,
			Parent: rng.IntN(9),
			Name:   []string{"a", "b", "c"}[rng.IntN(3)],
		})
	}
	slices.SortFunc(ops, func(a, b Op) int {
		if keyOf(a) == keyOf(b) {
			return 0
		}
		if lessKey(keyOf(a), keyOf(b)) {
			return -1
		}
		return 1
	})
	return ops
}

func applyInRandomOrder(t *testing.T, r *Replayer, ops []Op, order []int) {
	t.Helper()
	for _, idx := range order {
		op := ops[idx]
		if !validOp(op) {
			if _, err := r.Apply(op); err == nil || err.Error() != "invalid operation" {
				t.Fatalf("invalid op %+v returned %v", op, err)
			}
			continue
		}
		_, err := r.Apply(op)
		if err != nil {
			t.Fatalf("Apply(%+v): %v", op, err)
		}
		assertParentStepBound(t, r)
	}
}

func assertParentStepBound(t *testing.T, r *Replayer) {
	t.Helper()
	for node := range r.parent {
		if node < 2 {
			continue
		}
		steps := 1
		current := r.parent[node]
		for current != 0 && current != 1 {
			steps++
			if steps > len(r.parent) {
				t.Fatalf("node %d ancestor walk exceeded tree size", node)
			}
			current = r.parent[current]
		}
	}
}

func replayNaive(ops []Op) (map[int]nodeInfo, []LogEntry) {
	validOps := make([]Op, 0, len(ops))
	for _, op := range ops {
		if validOp(op) {
			validOps = append(validOps, op)
		}
	}
	slices.SortFunc(validOps, func(a, b Op) int {
		if keyOf(a) == keyOf(b) {
			return 0
		}
		if lessKey(keyOf(a), keyOf(b)) {
			return -1
		}
		return 1
	})

	parent := map[int]int{0: 0, 1: 1}
	name := map[int]string{}
	entries := make([]LogEntry, 0, len(validOps))
	for _, op := range validOps {
		effective := naiveApply(parent, name, op)
		entries = append(entries, LogEntry{Key: keyOf(op), Op: op, Effective: effective})
	}

	tree := make(map[int]nodeInfo)
	for node := range parent {
		if node >= 2 {
			tree[node] = nodeInfo{parent: parent[node], name: name[node]}
		}
	}
	return tree, entries
}

func naiveApply(parent map[int]int, name map[int]string, op Op) bool {
	if _, exists := parent[op.Parent]; !exists {
		return false
	}
	current := op.Parent
	for {
		if current == op.Node {
			return false
		}
		if current == 0 || current == 1 {
			parent[op.Node] = op.Parent
			name[op.Node] = op.Name
			return true
		}
		current = parent[current]
	}
}

func decisionBasis(r *Replayer, op Op) string {
	if _, exists := r.parent[op.Parent]; !exists {
		return "parent is absent"
	}
	current := op.Parent
	for current != 0 && current != 1 {
		if current == op.Node {
			return "parent chain reaches moved node"
		}
		current = r.parent[current]
	}
	if current == op.Node {
		return "parent chain reaches moved node"
	}
	return "parent exists and parent chain terminates at a root"
}

var _ = errors.Is
