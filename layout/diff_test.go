package layout

import (
	"fmt"
	"math/rand"
	"testing"
)

type recorder struct {
	events []string
}

// Random operation sequences run against both the incremental Tree and the
// independently written NaiveModel. After every commit/query the committed
// sizes of every node must agree with the full-recomputation oracle, and a
// second clean query must never reflow anything.
func TestRandomDifferentialVsNaive(t *testing.T) {
	const iterations = 60
	const ops = 400
	for iter := 0; iter < iterations; iter++ {
		r := rand.New(rand.NewSource(int64(iter*7919 + 1)))
		inc := quiet(NewTree(1))
		nav := NewNaiveModel(1)
		nextID := int64(2)

		// Node pool: some created-but-detached, to exercise retained dirt.
		allIDs := []int64{1}
		history := []string{}

		randomID := func() int64 { return allIDs[r.Intn(len(allIDs))] }
		mode := func() Mode {
			if r.Intn(2) == 0 {
				return fixed(r.Intn(5))
			}
			return content()
		}

		for op := 0; op < ops; op++ {
			switch r.Intn(9) {
			case 0:
				id := nextID
				nextID++
				w, h := mode(), mode()
				pad := r.Intn(3)
				iso := r.Intn(3) == 0
				e1 := inc.Create(id, w, h, pad, iso)
				e2 := nav.Create(id, w, h, pad, iso)
				requireSameErr(t, e1, e2)
				if e1 == nil {
					allIDs = append(allIDs, id)
				}
				history = append(history, fmt.Sprintf("create(%d,%v,%v,pad=%d,iso=%v):%v", id, w, h, pad, iso, e1 == nil))
			case 1, 2:
				id := randomID()
				m := mode()
				requireSameErr(t, inc.SetWidth(id, m), nav.SetWidth(id, m))
				history = append(history, fmt.Sprintf("setWidth(%d,%v)", id, m))
			case 3:
				id := randomID()
				m := mode()
				requireSameErr(t, inc.SetHeight(id, m), nav.SetHeight(id, m))
				history = append(history, fmt.Sprintf("setHeight(%d,%v)", id, m))
			case 4:
				id := randomID()
				pad := r.Intn(3)
				requireSameErr(t, inc.SetPadding(id, pad), nav.SetPadding(id, pad))
				history = append(history, fmt.Sprintf("setPad(%d,%d)", id, pad))
			case 5:
				id := randomID()
				iso := r.Intn(2) == 0
				requireSameErr(t, inc.SetIsolated(id, iso), nav.SetIsolated(id, iso))
				history = append(history, fmt.Sprintf("setIso(%d,%v)", id, iso))
			case 6:
				// Insert a detached candidate under a random attached parent.
				if detached := findDetached(inc); len(detached) > 0 {
					c := detached[r.Intn(len(detached))]
					pid := randomAttachedParent(inc, r)
					if pid > 0 {
						p, _ := inc.mustNode(pid)
						idx := r.Intn(len(p.children) + 1)
						requireSameErr(t, inc.Insert(pid, c, idx), nav.Insert(pid, c, idx))
						history = append(history, fmt.Sprintf("insert(%d under %d @%d)", c, pid, idx))
					}
				}
			case 7:
				// Move a random non-root attached node.
				if id := randomAttachedNonRoot(inc, r); id > 0 {
					n, _ := inc.mustNode(id)
					pid := randomAttachedParent(inc, r)
					if pid > 0 && !isDescendant(n, inc.nodes[pid]) {
						p, _ := inc.mustNode(pid)
						max := len(p.children)
						if n.parent == p {
							max--
						}
						if max >= 0 {
							idx := r.Intn(max + 1)
							requireSameErr(t, inc.Move(id, pid, idx), nav.Move(id, pid, idx))
							history = append(history, fmt.Sprintf("move(%d to %d @%d)", id, pid, idx))
						}
					}
				}
			case 8:
				if id := randomAttachedNonRoot(inc, r); id > 0 {
					requireSameErr(t, inc.Remove(id), nav.Remove(id))
					history = append(history, fmt.Sprintf("remove(%d)", id))
				}
			}

			// Occasionally commit or query on both models and compare sizes.
			if op%7 == 3 {
				_, e1 := inc.Commit()
				e2 := nav.Commit()
				requireSameErr(t, e1, e2)
			}
			if op%11 == 5 {
				id := randomID()
				if !connected(inc, inc.nodes[id]) {
					break
				}
				s1, e1 := inc.SizeOf(id)
				s2, e2 := nav.SizeOf(id)
				requireSameErr(t, e1, e2)
				if e1 == nil && s1 != s2 {
					start := op - 20
					if start < 0 {
						start = 0
					}
					t.Fatalf("iter %d op %d node %d: incremental %v naive %v\nrecent: %v",
						iter, op, id, s1, s2, history[start:])
				}
			}
		}

		// Final drain: commit everything and compare every attached node.
		_, _ = inc.Commit()
		for _, id := range allIDs {
			s1, e1 := inc.SizeOf(id)
			s2, e2 := nav.SizeOf(id)
			requireSameErr(t, e1, e2)
			if e1 == nil {
				if !connected(inc, inc.nodes[id]) {
					continue // detached nodes are outside both committed trees
				}
				if s1 != s2 {
					start := len(history) - 40
					if start < 0 {
						start = 0
					}
					t.Fatalf("final iter %d node %d: incremental %v naive %v\nrecent: %v",
						iter, id, s1, s2, history[start:])
				}
			}
		}
		// A clean query right after must reflow zero nodes.
		before := inc.LastReflowCount()
		_, _ = inc.SizeOf(1)
		if inc.LastReflowCount() != 0 || before != 0 {
			t.Fatalf("clean query reflowed nodes: %d", before)
		}
	}
}

func requireSameErr(t *testing.T, e1, e2 error) {
	t.Helper()
	if (e1 == nil) != (e2 == nil) {
		t.Fatalf("error mismatch: incremental=%v naive=%v", e1, e2)
	}
	if e1 != nil && errKind(e1) != errKind(e2) {
		t.Fatalf("error kind mismatch: %v vs %v", e1, e2)
	}
}

func findDetached(tr *Tree) []int64 {
	var out []int64
	for id, n := range tr.nodes {
		if id != 1 && n.parent == nil {
			out = append(out, id)
		}
	}
	return out
}

func connected(tr *Tree, n *node) bool {
	for p := n; p != nil; p = p.parent {
		if p == tr.root {
			return true
		}
	}
	return false
}

func randomAttachedNonRoot(tr *Tree, r *rand.Rand) int64 {
	var ids []int64
	for id, n := range tr.nodes {
		if n.parent != nil {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return 0
	}
	return ids[r.Intn(len(ids))]
}

func randomAttachedParent(tr *Tree, r *rand.Rand) int64 {
	var ids []int64
	for id, n := range tr.nodes {
		if id == 1 || n.parent != nil {
			ids = append(ids, id)
		}
	}
	return ids[r.Intn(len(ids))]
}

// Commit cost must not grow with the number of clean nodes: add a large clean
// forest, dirty a leaf inside one boundary and count recomputations.
func TestReflowCostIndependentOfCleanNodes(t *testing.T) {
	tr := quiet(NewTree(1))
	// Huge clean forest of fixed-sized leaves.
	for id := int64(2); id < 2000; id++ {
		m := fixed(1)
		if id == 1500 {
			m = content()
		}
		must(t, tr.Create(id, m, fixed(1), 0, false))
		must(t, tr.Insert(1, id, int(id-2)))
	}
	mustCommit(t, tr)

	must(t, tr.SetPadding(1500, 1))
	ch := mustCommit(t, tr)
	if len(ch) != 2 { // leaf 1500 and root
		t.Fatalf("expected exactly 2 recomputations, got %d (%v)", len(ch), ids(ch))
	}

	// Single-attribute propagation cost is bounded by depth to the nearest
	// boundary: here a boundary at depth 2 contains thousands of clean nodes.
	must(t, tr.Create(5000, fixed(9), fixed(9), 0, true))
	must(t, tr.Create(5001, content(), content(), 0, false))
	must(t, tr.Insert(1, 5000, 0))
	must(t, tr.Insert(5000, 5001, 0))
	mustCommit(t, tr)
	must(t, tr.SetPadding(5001, 1))
	ch = mustCommit(t, tr)
	if len(ch) != 2 { // 5001 and boundary 5000; root untouched
		t.Fatalf("expected dirt contained at shallow boundary, got %d nodes", len(ch))
	}
}
