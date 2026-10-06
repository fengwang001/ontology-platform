package sparse

import (
	"fmt"
	"testing"
)

// The engine exposes work counters (Stats). These tests prove the required
// complexity bounds by showing the counters stay constant while the number
// of unrelated files / unaffected materialized paths grows 100x. No wall
// clocks involved, so the proofs are deterministic.

func bigFiles(n int) []string {
	files := make([]string, 0, n+5)
	for i := 0; i < n; i++ {
		files = append(files, fmt.Sprintf("big/d%04d/f%05d", i%100, i))
	}
	for i := 1; i <= 5; i++ {
		files = append(files, fmt.Sprintf("small/f%d", i))
	}
	return files
}

func setupScaled(t *testing.T, n int) *Engine {
	t.Helper()
	e := NewEngine()
	if err := e.AddCommit("c1", bigFiles(n)); err != nil {
		t.Fatal(err)
	}
	v, err := e.AddRuleset([]Rule{Rule{Include, "big/"}, Rule{Include, "small/"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Apply("c1", v, false); err != nil {
		t.Fatal(err)
	}
	return e
}

// TestQueryCostIndependentOfTreeSize: single-path decisions must not grow
// with files unrelated to the path's own directory.
func TestQueryCostIndependentOfTreeSize(t *testing.T) {
	var measurements []StatsSnapshot
	for _, n := range []int{1_000, 100_000} {
		e := setupScaled(t, n)
		e.ResetStats()
		st, err := e.QueryPath("big/d0042/f00042")
		if err != nil {
			t.Fatal(err)
		}
		if st != StatusMaterialized {
			t.Fatalf("n=%d: status = %v, want materialized", n, st)
		}
		s := e.Stats()
		t.Logf("n=%d: QueryPath cost = %+v (依据: 单路径判定只走自身 trie 分支)", n, s)
		measurements = append(measurements, s)
	}
	if measurements[0] != measurements[1] {
		t.Errorf("query cost grew with tree size: %+v -> %+v", measurements[0], measurements[1])
	}
	if measurements[0].MatchSteps > 6 { // path depth is 3
		t.Errorf("match steps %d exceed O(depth) bound", measurements[0].MatchSteps)
	}
}

// TestChangeCostIndependentOfUnaffected: a ruleset change scoped to small/
// must not touch the huge materialized big/ subtree.
func TestChangeCostIndependentOfUnaffected(t *testing.T) {
	var measurements []StatsSnapshot
	for _, n := range []int{1_000, 100_000} {
		e := setupScaled(t, n)
		v2, err := e.AddRuleset([]Rule{
			Rule{Include, "big/"},
			Rule{Include, "small/"},
			Rule{Exclude, "small/f1"}, // appended: only this scope changed
		})
		if err != nil {
			t.Fatal(err)
		}
		e.ResetStats()
		res, err := e.Apply("", v2, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Removed) != 1 || res.Removed[0] != "small/f1" {
			t.Fatalf("n=%d: removed = %v, want [small/f1]", n, res.Removed)
		}
		s := e.Stats()
		t.Logf("n=%d: ruleset-change cost = %+v, kept=%d (依据: 变更开销与未受影响的已物化路径数无关)",
			n, s, res.Kept)
		measurements = append(measurements, s)
	}
	if measurements[0] != measurements[1] {
		t.Errorf("change cost grew with unaffected materialized paths: %+v -> %+v",
			measurements[0], measurements[1])
	}
}

// TestCommitSwitchCostIndependentOfUnaffected: switching to a commit that
// differs by one file must not walk the unchanged big/ subtree.
func TestCommitSwitchCostIndependentOfUnaffected(t *testing.T) {
	var measurements []StatsSnapshot
	for _, n := range []int{1_000, 100_000} {
		e := setupScaled(t, n)
		c2files := append(bigFiles(n), "small/f6")
		if err := e.AddCommit("c2", c2files); err != nil {
			t.Fatal(err)
		}
		e.ResetStats()
		res, err := e.Apply("c2", 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Added) != 1 || res.Added[0] != "small/f6" {
			t.Fatalf("n=%d: added = %v, want [small/f6]", n, res.Added)
		}
		s := e.Stats()
		t.Logf("n=%d: commit-switch cost = %+v (依据: Merkle 哈希剪枝, 未变子树不访问)", n, s)
		measurements = append(measurements, s)
	}
	if measurements[0] != measurements[1] {
		t.Errorf("commit-switch cost grew with tree size: %+v -> %+v", measurements[0], measurements[1])
	}
}

// TestListCostIndependentOfUnrelated: listing a small prefix must not touch
// unrelated parts of the tree.
func TestListCostIndependentOfUnrelated(t *testing.T) {
	var measurements []StatsSnapshot
	for _, n := range []int{1_000, 100_000} {
		e := setupScaled(t, n)
		e.ResetStats()
		got, err := e.ListMaterialized("small")
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 5 {
			t.Fatalf("n=%d: ListMaterialized(small) = %v", n, got)
		}
		s := e.Stats()
		t.Logf("n=%d: ListMaterialized(small) cost = %+v", n, s)
		measurements = append(measurements, s)
	}
	if measurements[0] != measurements[1] {
		t.Errorf("list cost grew with unrelated files: %+v -> %+v", measurements[0], measurements[1])
	}
}
