package layout

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
)

func content() Mode    { return Mode{Kind: ModeContent} }
func fixed(v int) Mode { return Mode{Kind: ModeFixed, Value: v} }

func quiet(tr *Tree) *Tree {
	tr.SetLogger(nil)
	return tr
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustCommit(t *testing.T, tr *Tree) []Change {
	t.Helper()
	ch, err := tr.Commit()
	must(t, err)
	return ch
}

func ids(ch []Change) []int64 {
	out := make([]int64, len(ch))
	for i, c := range ch {
		out[i] = c.NodeID
	}
	return out
}

func hasID(s []int64, id int64) bool {
	for _, x := range s {
		if x == id {
			return true
		}
	}
	return false
}

func containsAll(s []int64, want ...int64) bool {
	for _, id := range want {
		if !hasID(s, id) {
			return false
		}
	}
	return true
}

func equalIDs(a, b []int64) bool {
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

func errKind(err error) ErrorKind {
	if le, ok := err.(*LayoutError); ok {
		return le.Kind
	}
	return 0
}

// Boundary requires isolation + fixed width + fixed height. Each condition
// missing alone must let dirt cross the node toward its nearest boundary.
func TestBoundaryThreeConditionsEachMissing(t *testing.T) {
	type variant struct {
		name     string
		isolated bool
		w, h     Mode
		crosses  bool
	}
	variants := []variant{
		{"all-three", true, fixed(10), fixed(10), false},
		{"missing-isolation", false, fixed(10), fixed(10), true},
		{"missing-fixed-width", true, content(), fixed(10), true},
		{"missing-fixed-height", true, fixed(10), content(), true},
		{"missing-width-and-height", true, content(), content(), true},
	}
	for _, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			tr := quiet(NewTree(1))
			must(t, tr.Create(2, v.w, v.h, 0, v.isolated))
			must(t, tr.Create(3, content(), content(), 0, false))
			must(t, tr.Insert(1, 2, 0))
			must(t, tr.Insert(2, 3, 0))
			mustCommit(t, tr)

			must(t, tr.SetPadding(3, 1))
			dirty2, _ := tr.IsDirty(2)
			dirty1, _ := tr.IsDirty(1)
			if !dirty2 {
				t.Fatal("parent must be subtree-dirty")
			}
			if dirty1 != v.crosses {
				t.Fatalf("cross=%v: root dirt=%v", v.crosses, dirty1)
			}
			got := ids(mustCommit(t, tr))
			// Markers always follow boundary relation; actual damage only
			// crosses when at least one content dimension can change.
			contentAxis := v.w.Kind == ModeContent || v.h.Kind == ModeContent
			rootReflowed := hasID(got, 1)
			if rootReflowed != (v.crosses && contentAxis) {
				t.Fatalf("root reflow=%v for variant %s (set %v)", rootReflowed, v.name, got)
			}
		})
	}
}

// Root is always a boundary even with content sizing.
func TestRootIsAlwaysBoundary(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	mustCommit(t, tr)
	ok, err := tr.IsBoundary(1)
	must(t, err)
	if !ok {
		t.Fatal("root must be a boundary")
	}
	must(t, tr.SetPadding(2, 2))
	if got := ids(mustCommit(t, tr)); !equalIDs(got, []int64{2, 1}) {
		t.Fatalf("unexpected reflow set %v", got)
	}
}

// Setting an attribute to its current value creates no dirt and no reflow.
func TestSameValueChangeNoDirt(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, fixed(5), content(), 1, true))
	must(t, tr.Insert(1, 2, 0))
	mustCommit(t, tr)
	must(t, tr.SetWidth(2, fixed(5)))
	must(t, tr.SetHeight(2, content()))
	must(t, tr.SetPadding(2, 1))
	must(t, tr.SetIsolated(2, true))
	if d, _ := tr.IsDirty(2); d {
		t.Fatal("same-value changes must not dirty")
	}
	if ch := mustCommit(t, tr); len(ch) != 0 {
		t.Fatalf("expected no recomputation after same-value edits, got %v", ids(ch))
	}
}

// Damage stops at the first ancestor whose resulting size does not change.
func TestDamageStopsWhenParentSizeUnchanged(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, fixed(4), fixed(4), 0, false))
	must(t, tr.Create(3, fixed(4), fixed(4), 0, false))
	must(t, tr.Create(4, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	must(t, tr.Insert(1, 3, 1))
	must(t, tr.Insert(3, 4, 0))
	mustCommit(t, tr)

	must(t, tr.SetWidth(4, fixed(1)))
	ch := mustCommit(t, tr)
	got := ids(ch)
	if !equalIDs(got, []int64{4, 3}) {
		t.Fatalf("damage should stop at 3 (root width stays 8): %v", got)
	}
	for _, c := range ch {
		// Node 3 is fixed 4x4: it is recomputed because its child changed,
		// but its size stays 4x4, so damage must not reach the root.
		if c.NodeID == 3 && (c.Old != (Size{4, 4}) || c.New != (Size{4, 4})) {
			t.Fatalf("node 3 old/new wrong: %+v", c)
		}
	}
}

// Damage travels up a content-sized chain until it reaches a boundary.
func TestDamagePropagatesToBoundaryAndStops(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, content(), content(), 0, true))
	must(t, tr.Create(3, content(), content(), 0, false))
	must(t, tr.Create(4, fixed(10), fixed(10), 0, true)) // boundary
	must(t, tr.Create(5, content(), content(), 0, false))
	must(t, tr.Create(6, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	must(t, tr.Insert(2, 3, 0))
	must(t, tr.Insert(3, 4, 0))
	must(t, tr.Insert(4, 5, 0))
	must(t, tr.Insert(5, 6, 0))
	mustCommit(t, tr)

	must(t, tr.SetWidth(6, fixed(3)))
	got := ids(mustCommit(t, tr))
	if !equalIDs(got, []int64{6, 5, 4}) {
		t.Fatalf("damage should stop at boundary 4, got %v", got)
	}
	if d, _ := tr.IsDirty(3); d {
		t.Fatal("ancestors outside boundary must stay clean after commit")
	}
}

// Moving a dirty subtree across a boundary replays propagation at the new
// location; the moved subtree keeps its own marks.
func TestMoveSubtreeAcrossBoundaryRepropagates(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, fixed(20), fixed(20), 0, true)) // boundary
	must(t, tr.Create(3, content(), content(), 0, false))
	must(t, tr.Create(4, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	must(t, tr.Insert(2, 3, 0))
	must(t, tr.Insert(1, 4, 1))
	mustCommit(t, tr)

	must(t, tr.SetWidth(3, fixed(7))) // dirt contained in boundary 2
	must(t, tr.Move(3, 1, 1))         // 3 leaves boundary 2
	if d, _ := tr.IsDirty(1); !d {
		t.Fatal("escaping dirt must re-propagate to root after crossing a boundary")
	}
	got := ids(mustCommit(t, tr))
	if !containsAll(got, 2, 3, 1) {
		t.Fatalf("old parent 2, moved 3 and root must reflow, got %v", got)
	}
	if hasID(got, 4) {
		t.Fatalf("sibling 4 is unaffected by the move, got %v", got)
	}
}

// A removed subtree retains its dirt; reinsertion resumes propagation.
func TestRemoveAndReinsertKeepsDirt(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, content(), content(), 0, false))
	must(t, tr.Create(3, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	must(t, tr.Insert(2, 3, 0))
	mustCommit(t, tr)

	must(t, tr.SetPadding(3, 2))
	must(t, tr.Remove(2))
	mustCommit(t, tr)

	if d, _ := tr.IsDirty(3); !d {
		t.Fatal("detached subtree must retain dirty markers")
	}
	must(t, tr.SetPadding(3, 3))
	if d, _ := tr.IsDirty(1); d {
		t.Fatal("mutation inside a detached subtree must not reach the root")
	}

	must(t, tr.Insert(1, 2, 0))
	got := ids(mustCommit(t, tr))
	if !containsAll(got, 3, 2, 1) {
		t.Fatalf("reinserted dirt must reflow the subtree, got %v", got)
	}
	s3, _ := tr.SizeOf(3)
	if s3 != (Size{6, 6}) {
		t.Fatalf("node 3 should be 6x6, got %v", s3)
	}
}

// Losing boundary identity re-propagates contained dirt upward; gaining it
// never retracts ancestor marks.
func TestBoundaryIdentityLossAndGain(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, fixed(20), fixed(20), 0, true)) // boundary
	must(t, tr.Create(3, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))
	must(t, tr.Insert(2, 3, 0))
	mustCommit(t, tr)

	must(t, tr.SetPadding(3, 1))
	if d, _ := tr.IsDirty(1); d {
		t.Fatal("dirt must not cross boundary 2")
	}
	must(t, tr.SetIsolated(2, false))
	if d, _ := tr.IsDirty(1); !d {
		t.Fatal("after boundary loss, dirt must re-propagate to root")
	}
	mustCommit(t, tr)

	must(t, tr.SetPadding(3, 2))
	must(t, tr.SetIsolated(2, true))
	if d, _ := tr.IsDirty(1); !d {
		t.Fatal("gaining boundary identity must not retract ancestor marks")
	}
	ch := mustCommit(t, tr)
	if !containsAll(ids(ch), 3) {
		t.Fatalf("inner change must reflow, got %v", ids(ch))
	}
}

// Implicit commit on query matches an explicit commit; consecutive clean
// queries reflow nothing.
func TestImplicitEqualsExplicitCommit(t *testing.T) {
	build := func() *Tree {
		tr := quiet(NewTree(1))
		for _, id := range []int64{2, 3, 4} {
			must(t, tr.Create(id, content(), content(), 0, false))
		}
		must(t, tr.Insert(1, 2, 0))
		must(t, tr.Insert(1, 3, 1))
		must(t, tr.Insert(3, 4, 0))
		mustCommit(t, tr)
		return tr
	}
	explicit := build()
	implicit := build()

	must(t, explicit.SetPadding(4, 1))
	eCh := mustCommit(t, explicit)

	must(t, implicit.SetPadding(4, 1))
	s, err := implicit.SizeOf(4)
	must(t, err)
	if s != (Size{2, 2}) {
		t.Fatalf("node4 expected 2x2 got %v", s)
	}
	if n := implicit.LastReflowCount(); n != len(eCh) {
		t.Fatalf("implicit recomputed %d nodes, explicit %d", n, len(eCh))
	}
	_, _ = implicit.SizeOf(1)
	if n := implicit.LastReflowCount(); n != 0 {
		t.Fatalf("clean query reflowed %d nodes", n)
	}
	_, _ = implicit.SizeOf(1)
	if n := implicit.LastReflowCount(); n != 0 {
		t.Fatalf("second clean query reflowed %d nodes", n)
	}
}

// Rejection precedence: invalid argument < node-not-found < conflict <
// reentrant commit; rejected operations change nothing.
func TestErrorPrecedenceAndReentrantCommit(t *testing.T) {
	tr := quiet(NewTree(1))
	must(t, tr.Create(2, content(), content(), 0, false))
	must(t, tr.Insert(1, 2, 0))

	check := func(name string, want ErrorKind, fn func() error) {
		t.Helper()
		if got := errKind(fn()); got != want {
			t.Fatalf("%s: want kind %d got %d", name, want, got)
		}
	}
	check("negative size beats not-found", KindInvalidArgument,
		func() error { _, e := tr.SizeOf(0); return e })
	check("negative fixed value", KindInvalidArgument,
		func() error { return tr.SetWidth(999, fixed(-1)) })
	check("unknown mode", KindInvalidArgument,
		func() error { return tr.SetWidth(1, Mode{Kind: ModeKind(9)}) })
	check("self descendant insert", KindInvalidArgument,
		func() error { return tr.Insert(2, 1, 0) })
	check("not-found before conflict", KindNodeNotFound,
		func() error { return tr.Insert(2, 404, 0) })
	check("insert attached node conflict", KindConflict,
		func() error { return tr.Insert(1, 2, 0) })
	check("remove root conflict", KindConflict,
		func() error { return tr.Remove(1) })
	check("move root conflict", KindConflict,
		func() error { return tr.Move(1, 2, 0) })

	// Rejected operations left no dirt and changed sizes.
	mustCommit(t, tr)
	if d, _ := tr.IsDirty(1); d {
		t.Fatal("rejected operations must not mark anything dirty")
	}

	// Reentrant commit is rejected as its own class.
	tr2 := quiet(NewTree(1))
	must(t, tr2.Create(3, content(), content(), 0, false))
	must(t, tr2.Insert(1, 3, 0))
	must(t, tr2.SetPadding(3, 1))
	tr2.onCommit = func() {
		if _, e := tr2.Commit(); errKind(e) != KindReentrantCommit {
			t.Errorf("nested commit kind = %d, want %d", errKind(e), KindReentrantCommit)
		}
	}
	_ = mustCommit(t, tr2)
}

// Concurrent mutations serialize through the lock; after drain-and-commit
// every size equals a fresh full recomputation from current properties.
func TestConcurrentMutationsMatchFullRecompute(t *testing.T) {
	tr := quiet(NewTree(1))
	for id := int64(2); id <= 40; id++ {
		must(t, tr.Create(id, content(), content(), 0, id%7 == 0))
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for k := 0; k < 200; k++ {
				id := 2 + r.Int63n(39)
				switch r.Intn(3) {
				case 0:
					_ = tr.SetPadding(id, r.Intn(4))
				case 1:
					if r.Intn(2) == 0 {
						_ = tr.SetWidth(id, fixed(r.Intn(6)))
					} else {
						_ = tr.SetHeight(id, content())
					}
				default:
					_ = tr.SetIsolated(id, r.Intn(2) == 0)
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()
	mustCommit(t, tr)

	// Independent full recomputation from the same current properties.
	got := make(map[int64]Size)
	var full func(*node) Size
	full = func(n *node) Size {
		w, h := 2*n.pad, 2*n.pad
		for _, c := range n.children {
			cs := full(c)
			w += cs.W
			h += cs.H
		}
		if n.width.Kind == ModeFixed {
			w = n.width.Value
		}
		if n.height.Kind == ModeFixed {
			h = n.height.Value
		}
		got[n.id] = Size{w, h}
		return Size{w, h}
	}
	full(tr.root)
	for id := range tr.nodes {
		s, err := tr.SizeOf(id)
		must(t, err)
		if want, ok := got[id]; ok && s != want {
			t.Fatalf("node %d: committed %v want full %v", id, s, want)
		}
	}
}

// Logger receives inputs, outputs and decision rationales.
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	tr := NewTree(1)
	tr.SetLogger(StdLogger{W: &buf})
	must(t, tr.Create(2, content(), content(), 1, false))
	must(t, tr.Insert(1, 2, 0))
	_, err := tr.Commit()
	must(t, err)
	log := buf.String()
	for _, want := range []string{"[IN ]", "[OUT]", "[DEC]"} {
		if !bytes.Contains([]byte(log), []byte(want)) {
			t.Fatalf("log missing %s:\n%s", want, log)
		}
	}
}
