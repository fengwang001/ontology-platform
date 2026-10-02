package graphdirt

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func mustSetMtime(t *testing.T, d *Detector, path string, mtime int) {
	t.Helper()
	if err := d.SetMtime(path, mtime); err != nil {
		t.Fatalf("SetMtime(%q, %d): %v", path, mtime, err)
	}
}

func mustAdd(t *testing.T, d *Detector, edge Edge) {
	t.Helper()
	if err := d.AddEdge(edge); err != nil {
		t.Fatalf("AddEdge(%q): %v", edge.ID, err)
	}
}

func mustComplete(t *testing.T, d *Detector, edgeID string, outputs map[string]int) {
	t.Helper()
	if err := d.Complete(edgeID, outputs); err != nil {
		t.Fatalf("Complete(%q): %v", edgeID, err)
	}
}

func dirtyIDs(t *testing.T, d *Detector, targets []string) []string {
	t.Helper()
	ids, err := d.DirtySet(targets)
	if err != nil {
		t.Fatalf("DirtySet(%v): %v", targets, err)
	}
	return ids
}

func TestTimestampBoundaryAndMultipleOutputs(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 5)
	mustAdd(t, d, Edge{ID: "e", Command: "c", Outputs: []string{"a", "b"}, ExplicitInputs: []string{"src"}})
	mustComplete(t, d, "e", map[string]int{"a": 5, "b": 5})

	if got := dirtyIDs(t, d, []string{"a"}); len(got) != 0 {
		t.Fatalf("equal timestamps: dirty = %v, want none", got)
	}

	mustSetMtime(t, d, "b", 4)
	if got := dirtyIDs(t, d, []string{"a"}); !equalStrings(got, []string{"e"}) {
		t.Fatalf("minimum output one older: dirty = %v, want [e]", got)
	}

	mustSetMtime(t, d, "b", 5)
	if got := dirtyIDs(t, d, []string{"a"}); len(got) != 0 {
		t.Fatalf("minimum output caught up: dirty = %v, want none", got)
	}
}

func TestMissingLogAndCommandChange(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 1)
	mustAdd(t, d, Edge{ID: "e", Command: "c", Outputs: []string{"out"}, ExplicitInputs: []string{"src"}})

	if got := dirtyIDs(t, d, []string{"out"}); !equalStrings(got, []string{"e"}) {
		t.Fatalf("missing log: dirty = %v, want [e]", got)
	}

	mustComplete(t, d, "e", map[string]int{"out": 1})
	if got := dirtyIDs(t, d, []string{"out"}); len(got) != 0 {
		t.Fatalf("completed edge: dirty = %v, want none", got)
	}

	d.edges["e"].edge.Command = "new command"
	if got := dirtyIDs(t, d, []string{"out"}); !equalStrings(got, []string{"e"}) {
		t.Fatalf("changed command: dirty = %v, want [e]", got)
	}
}

func TestCompleteOutputSetAllowsRepeatedOutputEntry(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 2)
	mustAdd(t, d, Edge{ID: "e", Command: "c", Outputs: []string{"out", "out"}, ExplicitInputs: []string{"src"}})
	mustComplete(t, d, "e", map[string]int{"out": 2})

	if got := dirtyIDs(t, d, []string{"out"}); len(got) != 0 {
		t.Fatalf("repeated output entry: dirty = %v, want none", got)
	}
}

func TestRestatUsesLoggedInMax(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 10)
	mustAdd(t, d, Edge{ID: "r", Command: "c", Outputs: []string{"out"}, ExplicitInputs: []string{"src"}, Restat: true})
	mustComplete(t, d, "r", map[string]int{"out": 1})

	if got := dirtyIDs(t, d, []string{"out"}); len(got) != 0 {
		t.Fatalf("old restat output after caught-up inMax: dirty = %v, want none", got)
	}

	mustSetMtime(t, d, "src", 11)
	if got := dirtyIDs(t, d, []string{"out"}); !equalStrings(got, []string{"r"}) {
		t.Fatalf("restat input changed: dirty = %v, want [r]", got)
	}

	mustComplete(t, d, "r", map[string]int{"out": 1})
	if got := dirtyIDs(t, d, []string{"out"}); len(got) != 0 {
		t.Fatalf("restat recompleted with same output: dirty = %v, want none", got)
	}
}

func TestOrderOnlyInputsAndDirtyClosure(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "up-src", 1)
	mustSetMtime(t, d, "own-src", 1)
	mustSetMtime(t, d, "plain-order", 1)
	mustAdd(t, d, Edge{ID: "up", Command: "c", Outputs: []string{"up.out"}, ExplicitInputs: []string{"up-src"}})
	mustAdd(t, d, Edge{
		ID: "down", Command: "c", Outputs: []string{"down.out"},
		ExplicitInputs:  []string{"own-src"},
		OrderOnlyInputs: []string{"up.out", "plain-order"},
	})
	mustComplete(t, d, "up", map[string]int{"up.out": 1})
	mustComplete(t, d, "down", map[string]int{"down.out": 1})

	mustSetMtime(t, d, "plain-order", 2)
	if got := dirtyIDs(t, d, []string{"down.out"}); len(got) != 0 {
		t.Fatalf("changed order-only source: dirty = %v, want none", got)
	}

	mustSetMtime(t, d, "up-src", 2)
	if got := dirtyIDs(t, d, []string{"down.out"}); !equalStrings(got, []string{"up"}) {
		t.Fatalf("dirty order-only producer stays in closure: dirty = %v, want [up]", got)
	}
}

func TestDirtyPropagationAndRestatRebuildClearsDownstream(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 1)
	mustAdd(t, d, Edge{ID: "up", Command: "c", Outputs: []string{"up.out"}, ExplicitInputs: []string{"src"}, Restat: true})
	mustAdd(t, d, Edge{ID: "down", Command: "c", Outputs: []string{"down.out"}, ExplicitInputs: []string{"up.out"}})
	mustComplete(t, d, "up", map[string]int{"up.out": 1})
	mustComplete(t, d, "down", map[string]int{"down.out": 1})

	mustSetMtime(t, d, "src", 2)
	if got := dirtyIDs(t, d, []string{"down.out"}); !equalStrings(got, []string{"down", "up"}) {
		t.Fatalf("upstream dirt: dirty = %v, want [down up]", got)
	}

	mustComplete(t, d, "up", map[string]int{"up.out": 1})
	if got := dirtyIDs(t, d, []string{"down.out"}); len(got) != 0 {
		t.Fatalf("after restat upstream completion: dirty = %v, want none", got)
	}
}

func TestMissingSourceReportOrder(t *testing.T) {
	d := NewDetector()
	mustAdd(t, d, Edge{ID: "a", Command: "c", Outputs: []string{"a.out"}, OrderOnlyInputs: []string{"z-missing"}})
	mustAdd(t, d, Edge{ID: "b", Command: "c", Outputs: []string{"b.out"}, ExplicitInputs: []string{"a.out", "missing-b"}})

	var missing *MissingSourceError
	_, err := d.DirtySet([]string{"b.out"})
	if !errors.As(err, &missing) {
		t.Fatalf("error = %v, want MissingSourceError", err)
	}
	if missing.EdgeID != "a" || missing.Path != "z-missing" {
		t.Fatalf("missing source = (%q, %q), want (a, z-missing)", missing.EdgeID, missing.Path)
	}
}

func TestRejectionPriorities(t *testing.T) {
	t.Run("add edge", func(t *testing.T) {
		d := NewDetector()
		valid := Edge{ID: "e", Command: "c", Outputs: []string{"out"}, ExplicitInputs: []string{"cycle.out"}}
		mustAdd(t, d, valid)

		cases := []struct {
			edge Edge
			want error
		}{
			{Edge{}, ErrEmptyEdgeID},
			{Edge{ID: "e"}, ErrEdgeExists},
			{Edge{ID: "new"}, ErrNoOutputs},
			{Edge{ID: "new", Outputs: []string{""}}, ErrEmptyPath},
			{Edge{ID: "new", Outputs: []string{"out"}}, ErrOutputProduced},
			{Edge{ID: "new", Outputs: []string{"x"}, ExplicitInputs: []string{"x"}}, ErrInputOutputOverlap},
			{Edge{ID: "cycle", Outputs: []string{"cycle.out"}, ExplicitInputs: []string{"out"}}, ErrCycle},
		}
		for index, tc := range cases {
			if err := d.AddEdge(tc.edge); !errors.Is(err, tc.want) {
				t.Fatalf("case %d: error = %v, want %v", index, err, tc.want)
			}
		}
	})

	t.Run("complete", func(t *testing.T) {
		d := NewDetector()
		mustSetMtime(t, d, "src", 1)
		mustAdd(t, d, Edge{ID: "e", Command: "c", Outputs: []string{"out"}, ExplicitInputs: []string{"src"}})

		if err := d.Complete("missing", nil); !errors.Is(err, ErrEdgeNotFound) {
			t.Fatalf("missing edge: %v", err)
		}
		if err := d.Complete("e", map[string]int{"other": 1}); !errors.Is(err, ErrOutputSetMismatch) {
			t.Fatalf("wrong output set: %v", err)
		}
		if err := d.Complete("e", map[string]int{"out": 0}); !errors.Is(err, ErrInvalidMtime) {
			t.Fatalf("bad mtime: %v", err)
		}
		if err := d.Remove("src"); err != nil {
			t.Fatalf("Remove: %v", err)
		}
		if err := d.Complete("e", map[string]int{"out": 1}); !errors.Is(err, ErrMissingInput) {
			t.Fatalf("missing input: %v", err)
		}
	})

	t.Run("mtime and dirty", func(t *testing.T) {
		d := NewDetector()
		if err := d.SetMtime("", 0); !errors.Is(err, ErrEmptyPath) {
			t.Fatalf("SetMtime empty: %v", err)
		}
		if err := d.Remove(""); !errors.Is(err, ErrEmptyPath) {
			t.Fatalf("Remove empty: %v", err)
		}

		var target *TargetPathError
		_, err := d.DirtySet([]string{"unknown"})
		if !errors.As(err, &target) || target.Path != "unknown" {
			t.Fatalf("target error = %v, want unknown TargetPathError", err)
		}
	})
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func TestRandomGraphsMatchNaiveDefinition(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			d := NewDetector()
			edgeCount := 5 + rng.Intn(3)
			sourceCount := 2 + rng.Intn(4)
			edges := make([]Edge, edgeCount)

			for source := 0; source < sourceCount; source++ {
				path := fmt.Sprintf("s%d", source)
				mustSetMtime(t, d, path, 1+rng.Intn(12))
			}

			for index := range edges {
				edge := Edge{
					ID:      fmt.Sprintf("e%02d", index),
					Command: fmt.Sprintf("cmd-%d", rng.Intn(3)),
					Outputs: []string{fmt.Sprintf("o%d", index)},
					Restat:  rng.Intn(2) == 0,
				}
				edge.ExplicitInputs = randomInputs(rng, index, sourceCount, rng.Intn(3))
				edge.ImplicitInputs = randomInputs(rng, index, sourceCount, rng.Intn(2))
				edge.OrderOnlyInputs = randomInputs(rng, index, sourceCount, rng.Intn(3))
				mustAdd(t, d, edge)
				edges[index] = edge
			}

			for _, edge := range edges {
				if rng.Intn(5) == 0 {
					continue
				}
				outputTimes := map[string]int{}
				for _, output := range edge.Outputs {
					outputTimes[output] = 1 + rng.Intn(14)
				}
				_ = d.Complete(edge.ID, outputTimes)
			}

			for index := 0; index < sourceCount; index++ {
				path := fmt.Sprintf("s%d", index)
				switch rng.Intn(10) {
				case 0:
					if err := d.Remove(path); err != nil {
						t.Fatalf("Remove: %v", err)
					}
				case 1, 2:
					mustSetMtime(t, d, path, 1+rng.Intn(20))
				}
			}

			for index := range edges {
				if rng.Intn(8) == 0 {
					d.edges[edges[index].ID].edge.Command = "changed"
				}
			}

			targets := []string{fmt.Sprintf("o%d", edgeCount-1)}
			if edgeCount > 2 && rng.Intn(2) == 0 {
				targets = append(targets, fmt.Sprintf("o%d", edgeCount/2))
			}
			got, err := d.DirtySet(targets)
			naive, naiveErr := naiveDirtySet(d, targets)
			if err != nil {
				var gotMissing *MissingSourceError
				var wantMissing *MissingSourceError
				if !errors.As(err, &gotMissing) || !errors.As(naiveErr, &wantMissing) ||
					gotMissing.EdgeID != wantMissing.EdgeID || gotMissing.Path != wantMissing.Path {
					t.Fatalf("seed %d: missing-source error got %v, want %v", seed, err, naiveErr)
				}
				if testing.Verbose() {
					t.Logf("seed=%d inputs=%v outputs=%v targets=%v reason=missing-source edge=%s path=%s", seed, edges, d.mtimes, targets, gotMissing.EdgeID, gotMissing.Path)
				}
				return
			}
			if naiveErr != nil || !equalStrings(got, naive) {
				t.Fatalf("seed %d: dirty got %v, want %v (err got=%v want=%v)", seed, got, naive, err, naiveErr)
			}
			if testing.Verbose() {
				t.Logf("seed=%d inputs=%v outputs=%v targets=%v dirty=%v", seed, edges, d.mtimes, targets, got)
			}
		})
	}
}

func randomInputs(rng *rand.Rand, edgeIndex, sourceCount, count int) []string {
	inputs := make([]string, 0, count)
	for index := 0; index < count; index++ {
		if edgeIndex > 0 && rng.Intn(2) == 0 {
			inputs = append(inputs, fmt.Sprintf("o%d", rng.Intn(edgeIndex)))
		} else {
			inputs = append(inputs, fmt.Sprintf("s%d", rng.Intn(sourceCount)))
		}
	}
	return inputs
}

func naiveDirtySet(d *Detector, targets []string) ([]string, error) {
	closure := make(map[string]struct{})
	pending := make([]string, 0)
	for _, target := range targets {
		if edgeID, exists := d.producer[target]; exists {
			pending = append(pending, edgeID)
		}
	}
	for len(pending) > 0 {
		edgeID := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := closure[edgeID]; seen {
			continue
		}
		closure[edgeID] = struct{}{}
		edge := d.edges[edgeID].edge
		for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs, edge.OrderOnlyInputs} {
			for _, path := range list {
				if producer, exists := d.producer[path]; exists {
					pending = append(pending, producer)
				}
			}
		}
	}

	ids := make([]string, 0, len(closure))
	for edgeID := range closure {
		ids = append(ids, edgeID)
	}
	sort.Strings(ids)

	for _, edgeID := range ids {
		edge := d.edges[edgeID].edge
		for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs, edge.OrderOnlyInputs} {
			for _, path := range list {
				if _, produced := d.producer[path]; !produced {
					if _, exists := d.mtimes[path]; !exists {
						return nil, &MissingSourceError{EdgeID: edgeID, Path: path}
					}
				}
			}
		}
	}

	var isDirty func(edgeID string) bool
	isDirty = func(edgeID string) bool {
		result := naiveSelfDirty(d, d.edges[edgeID])
		edge := d.edges[edgeID].edge
		for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs} {
			for _, path := range list {
				if producer, exists := d.producer[path]; exists && isDirty(producer) {
					result = true
				}
			}
		}
		return result
	}

	result := make([]string, 0)
	for _, edgeID := range ids {
		if isDirty(edgeID) {
			result = append(result, edgeID)
		}
	}
	return result, nil
}

func naiveSelfDirty(d *Detector, state *edgeState) bool {
	edge := state.edge
	log, completed := d.logs[edge.ID]
	if !completed || log.Command != edge.Command {
		return true
	}
	minOutput := 0
	for index, output := range edge.Outputs {
		t, exists := d.mtimes[output]
		if !exists {
			return true
		}
		if index == 0 || t < minOutput {
			minOutput = t
		}
	}
	inputMax := 0
	for _, list := range [][]string{edge.ExplicitInputs, edge.ImplicitInputs} {
		for _, path := range list {
			if t, exists := d.mtimes[path]; exists && t > inputMax {
				inputMax = t
			}
		}
	}
	if edge.Restat {
		return log.InMax < inputMax
	}
	return minOutput < inputMax
}

func TestEvaluationCountAndDeepLinearChain(t *testing.T) {
	for _, edgeCount := range []int{1000, 20000} {
		d := NewDetector()
		mustSetMtime(t, d, "src", 1)
		mustAdd(t, d, Edge{ID: "e000000", Command: "c", Outputs: []string{"o0"}, ExplicitInputs: []string{"src"}})
		mustComplete(t, d, "e000000", map[string]int{"o0": 1})
		for index := 1; index < edgeCount; index++ {
			id := fmt.Sprintf("e%07d", index)
			input := fmt.Sprintf("o%d", index-1)
			output := fmt.Sprintf("o%d", index)
			mustAdd(t, d, Edge{ID: id, Command: "c", Outputs: []string{output}, ExplicitInputs: []string{input}})
			mustComplete(t, d, id, map[string]int{output: 1})
		}

		d.resetEvalCount()
		ids, err := d.DirtySet([]string{fmt.Sprintf("o%d", edgeCount-1)})
		if err != nil {
			t.Fatalf("n=%d: %v", edgeCount, err)
		}
		if len(ids) != 0 {
			t.Fatalf("n=%d: dirty = %v, want none", edgeCount, ids)
		}
		if count := d.evalCountValue(); count != int64(edgeCount) {
			t.Fatalf("n=%d: eval count = %d, want %d", edgeCount, count, edgeCount)
		}
	}
}

func TestSharedDiamondEvaluationCount(t *testing.T) {
	for _, edgeCount := range []int{1000, 20000} {
		d := NewDetector()
		mustSetMtime(t, d, "a", 1)
		mustSetMtime(t, d, "b", 1)
		mustAdd(t, d, Edge{ID: "a0", Command: "c", Outputs: []string{"d0"}, ExplicitInputs: []string{"a", "b"}})
		mustComplete(t, d, "a0", map[string]int{"d0": 1})
		mustAdd(t, d, Edge{ID: "a1", Command: "c", Outputs: []string{"d1"}, ExplicitInputs: []string{"a", "b"}})
		mustComplete(t, d, "a1", map[string]int{"d1": 1})
		for index := 2; index < edgeCount; index++ {
			id := fmt.Sprintf("a%d", index)
			mustAdd(t, d, Edge{
				ID: id, Command: "c", Outputs: []string{fmt.Sprintf("d%d", index)},
				ExplicitInputs: []string{fmt.Sprintf("d%d", index-1), fmt.Sprintf("d%d", index-2)},
			})
			mustComplete(t, d, id, map[string]int{fmt.Sprintf("d%d", index): 1})
		}

		d.resetEvalCount()
		ids, err := d.DirtySet([]string{fmt.Sprintf("d%d", edgeCount-1)})
		if err != nil {
			t.Fatalf("n=%d: %v", edgeCount, err)
		}
		if len(ids) != 0 {
			t.Fatalf("n=%d dirty = %v", edgeCount, ids)
		}
		if count := d.evalCountValue(); count != int64(edgeCount) {
			t.Fatalf("n=%d eval count = %d, want %d", edgeCount, count, edgeCount)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	d := NewDetector()
	mustSetMtime(t, d, "src", 1)
	mustAdd(t, d, Edge{ID: "e", Command: "c", Outputs: []string{"out"}, ExplicitInputs: []string{"src"}})
	if err := d.Complete("e", map[string]int{"out": 1}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for index := 0; index < 100; index++ {
				_ = d.SetMtime("src", worker+index+1)
				_, _ = d.DirtySet([]string{"out"})
			}
		}(worker)
	}
	wait.Wait()
}
