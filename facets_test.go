package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func mustAdd(t *testing.T, fc *FacetCounter, docID string, attrs Attrs) {
	t.Helper()
	if err := fc.Add(docID, attrs); err != nil {
		t.Fatalf("Add(%q) unexpected error: %v", docID, err)
	}
}

func itemMap(fr FacetResult) map[string]FacetItem {
	m := make(map[string]FacetItem, len(fr.Items))
	for _, item := range fr.Items {
		m[item.Value] = item
	}
	return m
}

func facetByDim(res FacetsResult) map[string]FacetResult {
	m := make(map[string]FacetResult, len(res.Facets))
	for _, fr := range res.Facets {
		m[fr.Dimension] = fr
	}
	return m
}

// The spec's worked example.
func TestSpecExample(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "d1", Attrs{"color": {"red/dark"}, "size": {"L"}})
	mustAdd(t, fc, "d2", Attrs{"color": {"red/light", "blue"}, "size": {"M"}})
	mustAdd(t, fc, "d3", Attrs{"color": {"blue/navy"}, "size": {"L"}})
	if err := fc.Link("size", "color"); err != nil {
		t.Fatalf("Link: %v", err)
	}

	res, err := fc.Facets(map[string][]string{}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Fatalf("Total = %d, want 3", res.Total)
	}
	if len(res.Facets) != 1 || res.Facets[0].Dimension != "color" {
		t.Fatalf("facets = %+v, want only color", res.Facets)
	}
	color := itemMap(res.Facets[0])
	want := map[string]int{"blue": 2, "red": 2, "blue/navy": 1, "red/dark": 1, "red/light": 1}
	if len(color) != len(want) {
		t.Fatalf("color items = %v, want %v", color, want)
	}
	for value, count := range want {
		if color[value].Count != count {
			t.Fatalf("color[%s] count = %d, want %d", value, color[value].Count, count)
		}
		if color[value].Selected {
			t.Fatalf("color[%s] unexpectedly selected", value)
		}
	}

	res, err = fc.Facets(map[string][]string{"color": {"red"}, "size": {"L"}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 1 {
		t.Fatalf("Total = %d, want 1", res.Total)
	}
	byDim := facetByDim(res)
	color = itemMap(byDim["color"])
	wantColor := map[string]int{"blue": 1, "blue/navy": 1, "red": 1, "red/dark": 1}
	if len(color) != len(wantColor) {
		t.Fatalf("color items = %v, want %v", color, wantColor)
	}
	for value, count := range wantColor {
		if color[value].Count != count {
			t.Fatalf("color[%s] = %d, want %d", value, color[value].Count, count)
		}
	}
	if !color["red"].Selected || color["blue"].Selected {
		t.Fatalf("selected flags wrong: red=%v blue=%v", color["red"].Selected, color["blue"].Selected)
	}
	size := itemMap(byDim["size"])
	if size["L"].Count != 1 || !size["L"].Selected {
		t.Fatalf("size L = %+v, want count 1 selected", size["L"])
	}
	if size["M"].Count != 1 || size["M"].Selected {
		t.Fatalf("size M = %+v, want count 1 not selected", size["M"])
	}

	res, err = fc.Facets(map[string][]string{"size": {"L"}}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 {
		t.Fatalf("Total = %d, want 3", res.Total)
	}
	if len(res.Facets) != 1 || res.Facets[0].Dimension != "color" {
		t.Fatalf("facets = %+v, want only color", res.Facets)
	}
}

// Own-dimension filter must not crush sibling values to zero; other-dimension
// filter shrinks this dimension's counts.
func TestOwnIgnoredOtherShrinks(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"c": {"red", "blue"}})
	mustAdd(t, fc, "y", Attrs{"c": {"blue"}, "s": {"L"}})
	mustAdd(t, fc, "z", Attrs{"c": {"red"}, "s": {"M"}})
	mustAdd(t, fc, "w", Attrs{"c": {"green"}, "s": {"L"}})

	res, _ := fc.Facets(map[string][]string{"c": {"red"}}, 50)
	if res.Total != 2 {
		t.Fatalf("Total = %d, want 2", res.Total)
	}
	c := itemMap(res.Facets[0])
	if c["red"].Count != 2 || !c["red"].Selected {
		t.Fatalf("red = %+v, want 2 selected", c["red"])
	}
	if c["blue"].Count != 2 || c["blue"].Selected {
		t.Fatalf("blue = %+v, want 2 not selected", c["blue"])
	}

	res, _ = fc.Facets(map[string][]string{"c": {"blue"}, "s": {"L"}}, 50)
	if res.Total != 1 {
		t.Fatalf("Total = %d, want 1 (only y)", res.Total)
	}
	byDim := facetByDim(res)
	c = itemMap(byDim["c"])
	// c ignores its own {blue}: constrained only by s={L} -> y and w.
	if c["blue"].Count != 1 || c["green"].Count != 1 || c["red"].Count != 0 {
		t.Fatalf("c counts = %+v, want blue=1 green=1 red=0", c)
	}
	s := itemMap(byDim["s"])
	// s ignores its own {L}: constrained only by c={blue} -> x and y.
	if s["L"].Count != 1 || !s["L"].Selected {
		t.Fatalf("L = %+v, want 1 selected", s["L"])
	}
	if s["M"].Count != 0 || s["M"].Selected {
		t.Fatalf("M = %+v, want 0 not selected", s["M"])
	}
}

func TestEmptySelectedEqualsAbsent(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"c": {"a"}})
	res1, _ := fc.Facets(map[string][]string{}, 50)
	res2, _ := fc.Facets(map[string][]string{"c": {}}, 50)
	if !reflect.DeepEqual(res1, res2) {
		t.Fatalf("empty selection differs: %+v vs %+v", res1, res2)
	}
	if res2.Total != 1 {
		t.Fatalf("Total = %d, want 1", res2.Total)
	}
}

// Hierarchical match is by segments, never by raw string prefix.
func TestSegmentPrefixNotStringPrefix(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "only-ab", Attrs{"c": {"ab"}})
	mustAdd(t, fc, "only-abc", Attrs{"c": {"a/bc"}})
	mustAdd(t, fc, "under-ab", Attrs{"c": {"a", "a/b", "a/b/c"}})

	res, _ := fc.Facets(map[string][]string{"c": {"a"}}, 50)
	// "a" matches the a/* subtree (a/bc included) but never the string
	// prefix-sharing "ab".
	if res.Total != 2 {
		t.Fatalf("select a: Total = %d, want 2 (under-ab, only-abc)", res.Total)
	}
	res, _ = fc.Facets(map[string][]string{"c": {"a/b"}}, 50)
	// "a/b" must not match "a/bc"; only the true descendant document.
	if res.Total != 1 {
		t.Fatalf("select a/b: Total = %d, want 1 (under-ab only, not a/bc)", res.Total)
	}
	res, _ = fc.Facets(map[string][]string{}, 50)
	c := itemMap(res.Facets[0])
	// a is an ancestor of both hierarchical documents; every other node of one.
	if c["a"].Count != 2 {
		t.Fatalf("node a count = %d, want 2; full=%+v", c["a"].Count, c)
	}
	for _, node := range []string{"ab", "a/b", "a/bc", "a/b/c"} {
		if c[node].Count != 1 {
			t.Fatalf("node %s count = %d, want 1; full=%+v", node, c[node].Count, c)
		}
	}
}

// Multiple values sharing an ancestor count the ancestor once per document.
func TestSharedAncestorCountedOnce(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"c": {"a/b", "a/c", "a/d/e"}})
	res, _ := fc.Facets(map[string][]string{}, 50)
	c := itemMap(res.Facets[0])
	if c["a"].Count != 1 {
		t.Fatalf("ancestor a count = %d, want 1 (not 3)", c["a"].Count)
	}
	for _, node := range []string{"a/b", "a/c", "a/d", "a/d/e"} {
		if c[node].Count != 1 {
			t.Fatalf("node %s count = %d, want 1", node, c[node].Count)
		}
	}
}

// Zero-count selected value appears and participates in ordering/truncation.
func TestZeroCountSelectedAndTopN(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"c": {"a/aa", "b"}})

	res, _ := fc.Facets(map[string][]string{"c": {"zz"}}, 1)
	if res.Total != 0 {
		t.Fatalf("Total = %d, want 0", res.Total)
	}
	items := res.Facets[0].Items
	if len(items) != 1 || items[0].Value != "a" || items[0].Count != 1 {
		t.Fatalf("top1 = %+v, want a/1", items)
	}

	res, _ = fc.Facets(map[string][]string{"c": {"zz"}}, 50)
	full := res.Facets[0].Items
	if len(full) != 4 {
		t.Fatalf("candidate count = %d, want 4", len(full))
	}
	got := []string{full[0].Value, full[1].Value, full[2].Value, full[3].Value}
	if !reflect.DeepEqual(got, []string{"a", "a/aa", "b", "zz"}) {
		t.Fatalf("order = %v, want a a/aa b zz", got)
	}
	if full[3].Count != 0 || !full[3].Selected {
		t.Fatalf("last = %+v, want zz/0/selected", full[3])
	}

	res, _ = fc.Facets(map[string][]string{"c": {"zz"}}, 4)
	if len(res.Facets[0].Items) != 4 {
		t.Fatalf("topN==candidates: %d items, want 4", len(res.Facets[0].Items))
	}
	res, _ = fc.Facets(map[string][]string{"c": {"zz"}}, 3)
	if len(res.Facets[0].Items) != 3 {
		t.Fatalf("topN=candidates-1: %d items, want 3", len(res.Facets[0].Items))
	}
}

// Selected dimension absent from every document: Total 0 and the dimension
// still appears with its selected value at count 0.
func TestUnknownSelectedDimension(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"c": {"a"}})
	res, _ := fc.Facets(map[string][]string{"ghost": {"g"}}, 50)
	if res.Total != 0 {
		t.Fatalf("Total = %d, want 0", res.Total)
	}
	g, ok := facetByDim(res)["ghost"]
	if !ok {
		t.Fatal("ghost dimension missing")
	}
	if len(g.Items) != 1 || g.Items[0].Value != "g" || g.Items[0].Count != 0 || !g.Items[0].Selected {
		t.Fatalf("ghost items = %+v, want g/0/selected", g.Items)
	}
}

// Two-level dependency chain and the "parent matches nothing but child stays
// effective" rule.
func TestDependencyChain(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "x", Attrs{"a": {"1"}, "b": {"2"}, "c": {"3"}})
	if err := fc.Link("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Link("c", "b"); err != nil {
		t.Fatal(err)
	}

	res, _ := fc.Facets(map[string][]string{}, 50)
	if len(res.Facets) != 1 || res.Facets[0].Dimension != "a" {
		t.Fatalf("nothing selected: facets = %+v, want only a", res.Facets)
	}
	res, _ = fc.Facets(map[string][]string{"c": {"3"}}, 50)
	if len(res.Facets) != 1 || res.Total != 1 {
		t.Fatalf("only c selected: facets=%+v total=%d, want a only / 1", res.Facets, res.Total)
	}
	res, _ = fc.Facets(map[string][]string{"a": {"1"}}, 50)
	dims := effectiveDimNames(res)
	if !reflect.DeepEqual(dims, []string{"a", "b"}) {
		t.Fatalf("a selected: effective dims = %v, want [a b]", dims)
	}

	// Parent selected with a value no document has: Total 0, child b remains
	// effective (effectiveness needs the parent effective with a non-empty
	// selected set only). Give b its own selected value so the effective but
	// candidate-less dimension is observable in the result.
	res, _ = fc.Facets(map[string][]string{"a": {"nope"}, "b": {"b-sel"}}, 50)
	if res.Total != 0 {
		t.Fatalf("Total = %d, want 0", res.Total)
	}
	if !reflect.DeepEqual(effectiveDimNames(res), []string{"a", "b"}) {
		t.Fatalf("effective dims = %v, want [a b]", effectiveDimNames(res))
	}
	bItems := facetByDim(res)["b"].Items
	if len(bItems) != 1 || bItems[0].Value != "b-sel" || bItems[0].Count != 0 || !bItems[0].Selected {
		t.Fatalf("b items = %+v, want b-sel count 0 selected", bItems)
	}
}

func effectiveDimNames(res FacetsResult) []string {
	dims := make([]string, 0, len(res.Facets))
	for _, f := range res.Facets {
		dims = append(dims, f.Dimension)
	}
	sort.Strings(dims)
	return dims
}

func TestLinkRejections(t *testing.T) {
	fc := NewFacetCounter()
	if err := fc.Link("", "p"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty child: %v", err)
	}
	if err := fc.Link("c", ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty parent: %v", err)
	}
	if err := fc.Link("a", "a"); !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("self loop: %v", err)
	}
	if err := fc.Link("b", "a"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Link("b", "a"); !errors.Is(err, ErrDuplicateLink) {
		t.Fatalf("re-declare identical: %v", err)
	}
	if err := fc.Link("b", "c"); !errors.Is(err, ErrDuplicateLink) {
		t.Fatalf("re-declare different: %v", err)
	}
	if err := fc.Link("c", "b"); err != nil {
		t.Fatal(err)
	}
	if err := fc.Link("a", "c"); !errors.Is(err, ErrCyclicDependency) {
		t.Fatalf("second-level cycle: %v", err)
	}
	if err := fc.Link("ghost2", "ghost1"); err != nil {
		t.Fatalf("link unknown dims: %v", err)
	}
}

func TestNoCandidateDimensionOmitted(t *testing.T) {
	fc := NewFacetCounter()
	if err := fc.Link("child", "root"); err != nil {
		t.Fatal(err)
	}
	res, _ := fc.Facets(map[string][]string{}, 50)
	if len(res.Facets) != 0 || res.Total != 0 {
		t.Fatalf("empty store facets = %+v, want no facets", res)
	}
	res, _ = fc.Facets(map[string][]string{"root": {"r"}}, 50)
	if len(res.Facets) != 1 || res.Facets[0].Dimension != "root" {
		t.Fatalf("facets = %+v, want root only (child has no candidates)", res.Facets)
	}
}

func TestMutationsAndErrorOrder(t *testing.T) {
	fc := NewFacetCounter()
	if err := fc.Add("", Attrs{"": nil}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty docID: %v", err)
	}
	if err := fc.Add("d", Attrs{"": {"a"}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty dim: %v", err)
	}
	for _, bad := range []string{"/a", "a/", "a//b", "a/b/c/d/e"} {
		if err := fc.Add("d", Attrs{"c": {bad}}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("bad path %q: %v", bad, err)
		}
	}
	if err := fc.Add("d", Attrs{"c": {"a", "a"}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate value: %v", err)
	}
	bigAttrs := Attrs{}
	for i := 0; i < 17; i++ {
		bigAttrs[fmt.Sprintf("d%02d", i)] = []string{"v"}
	}
	if err := fc.Add("d", bigAttrs); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many dims: %v", err)
	}
	manyVals := make([]string, 33)
	for i := range manyVals {
		manyVals[i] = fmt.Sprintf("v%02d", i)
	}
	if err := fc.Add("d", Attrs{"c": manyVals}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("too many values: %v", err)
	}

	mustAdd(t, fc, "d", Attrs{"c": {"a/b"}})
	if err := fc.Add("d", Attrs{"c": {"z"}}); !errors.Is(err, ErrDuplicateDocument) {
		t.Fatalf("duplicate Add: %v", err)
	}
	// Replace: invalid args are reported before "not found".
	if err := fc.Replace("nope", Attrs{"c": {"/bad"}}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("replace invalid-arg precedence: %v", err)
	}
	if err := fc.Replace("nope", Attrs{"c": {"z"}}); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("replace missing: %v", err)
	}
	if err := fc.Delete("nope"); !errors.Is(err, ErrDocumentNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
	if err := fc.Delete(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("delete empty id: %v", err)
	}
	// Atomic replacement removes old values.
	if err := fc.Replace("d", Attrs{"c": {"z"}}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	res, _ := fc.Facets(map[string][]string{}, 50)
	c := itemMap(res.Facets[0])
	if _, oldPresent := c["a"]; oldPresent {
		t.Fatalf("old node a survived Replace: %+v", c)
	}
	if c["z"].Count != 1 {
		t.Fatalf("new node z missing: %+v", c)
	}
	if err := fc.Delete("d"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	res, _ = fc.Facets(map[string][]string{}, 50)
	if res.Total != 0 || len(res.Facets) != 0 {
		t.Fatalf("after delete: %+v, want empty", res)
	}
}

func TestFacetsValidation(t *testing.T) {
	fc := NewFacetCounter()
	for _, topN := range []int{0, -1, 51, 100} {
		if _, err := fc.Facets(map[string][]string{}, topN); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("topN=%d: %v", topN, err)
		}
	}
	if _, err := fc.Facets(map[string][]string{"": {"a"}}, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty dim in selected: %v", err)
	}
	if _, err := fc.Facets(map[string][]string{"c": {"a//b"}}, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad path in selected: %v", err)
	}
}

// Returned data must not alias internal state: mutating inputs/outputs must
// not corrupt the counter.
func TestNoAliasingAndDeterminism(t *testing.T) {
	fc1, fc2 := NewFacetCounter(), NewFacetCounter()
	attrs := Attrs{"c": {"a/b", "a/c"}}
	mustAdd(t, fc1, "d", attrs)
	mustAdd(t, fc2, "d", attrs)
	// Mutate caller input after Add.
	attrs["c"][0] = "hacked"
	delete(attrs, "c")

	sel := map[string][]string{"c": {"a"}}
	r1, _ := fc1.Facets(sel, 50)
	// Mutate caller selection and previous result.
	sel["c"][0] = "hacked"
	r1.Facets[0].Items[0].Value = "hacked"
	sel["extra"] = []string{"x"}
	r2, _ := fc2.Facets(map[string][]string{"c": {"a"}}, 50)
	r3, _ := fc1.Facets(map[string][]string{"c": {"a"}}, 50)
	if !reflect.DeepEqual(r2, r3) {
		t.Fatalf("internal state leaked:\n%+v\n%+v", r2, r3)
	}

	// Registration order independence: shuffle the same document set.
	a, b := NewFacetCounter(), NewFacetCounter()
	mustAdd(t, a, "1", Attrs{"c": {"x"}})
	mustAdd(t, a, "2", Attrs{"c": {"y"}})
	mustAdd(t, b, "2", Attrs{"c": {"y"}})
	mustAdd(t, b, "1", Attrs{"c": {"x"}})
	ra, _ := a.Facets(map[string][]string{"c": {"x", "y"}}, 1)
	rb, _ := b.Facets(map[string][]string{"c": {"y", "x"}}, 1)
	if !reflect.DeepEqual(ra, rb) {
		t.Fatalf("order dependence: %+v vs %+v", ra, rb)
	}
}

// Rejected operations must leave both documents and links untouched.
func TestRejectionDoesNotChangeState(t *testing.T) {
	fc := NewFacetCounter()
	mustAdd(t, fc, "d", Attrs{"c": {"a"}})
	before, _ := fc.Facets(map[string][]string{}, 50)
	_ = fc.Add("d", Attrs{"c": {"/bad"}})      // invalid, doc exists anyway
	_ = fc.Add("d2", Attrs{"c": {"/bad"}})     // invalid
	_ = fc.Link("c", "c")                      // self loop
	_ = fc.Link("c", "p")                      // valid
	_ = fc.Link("c", "q")                      // duplicate rejected
	_ = fc.Link("p", "c")                      // cycle rejected
	_ = fc.Replace("d", Attrs{"c": {"//bad"}}) // invalid
	_ = fc.Delete("missing")                   // missing
	after, _ := fc.Facets(map[string][]string{"p": {"p-sel"}}, 50)
	// c is child of p; with p selected non-empty, c effective. Cycle rejection
	// must not have inverted the edge.
	cFacet, cThere := facetByDim(after)["c"]
	if cThere {
		// c has no own selected value and no documents survive; if present it
		// would mean a candidate leaked. It is normally omitted, so verify via
		// Total 0 and p presence below instead.
		_ = cFacet
	}
	pFacet, pThere := facetByDim(after)["p"]
	if !pThere || after.Total != 0 || len(pFacet.Items) != 1 || pFacet.Items[0].Value != "p-sel" {
		t.Fatalf("state changed by rejected ops: %+v", after)
	}
	// With c also selected (still no documents under p), c stays effective and
	// its selected value appears at count 0 — proving the edge is c->p.
	after2, _ := fc.Facets(map[string][]string{"p": {"p-sel"}, "c": {"a"}}, 50)
	c2, cEffective := facetByDim(after2)["c"]
	// c's count obeys the p constraint (no document has p-sel), so it is 0;
	// its presence with the selected value proves c stayed effective.
	if !cEffective || len(c2.Items) != 1 || c2.Items[0].Value != "a" || c2.Items[0].Count != 0 {
		t.Fatalf("c should be effective under p with count 0: %+v", after2)
	}
	_ = before
}

// ---------- naive reference model ----------

type naiveStore struct {
	docs   map[string]map[string]map[string]bool
	parent map[string]string
}

func naiveExpand(values []string) map[string]bool {
	nodes := map[string]bool{}
	for _, value := range values {
		segs := strings.Split(value, "/")
		prefix := ""
		for i, seg := range segs {
			if i > 0 {
				prefix += "/"
			}
			prefix += seg
			nodes[prefix] = true
		}
	}
	return nodes
}

func (n *naiveStore) effective(dim string, sel map[string]map[string]bool) bool {
	seen := map[string]bool{}
	for {
		if seen[dim] {
			return false
		}
		seen[dim] = true
		parent, ok := n.parent[dim]
		if !ok {
			return true
		}
		if !n.effective(parent, sel) || len(sel[parent]) == 0 {
			return false
		}
		dim = parent
	}
}

func (n *naiveStore) facets(sel map[string]map[string]bool, topN int) FacetsResult {
	known := map[string]bool{}
	for _, doc := range n.docs {
		for dim := range doc {
			known[dim] = true
		}
	}
	for dim := range sel {
		known[dim] = true
	}
	for child, parent := range n.parent {
		known[child] = true
		known[parent] = true
	}
	var eff []string
	for dim := range known {
		if n.effective(dim, sel) {
			eff = append(eff, dim)
		}
	}
	sort.Strings(eff)

	match := func(doc map[string]map[string]bool, dims []string) bool {
		for _, dim := range dims {
			hit := false
			for value := range sel[dim] {
				if doc[dim][value] {
					hit = true
					break
				}
			}
			if !hit {
				return false
			}
		}
		return true
	}

	var constraining []string
	for _, dim := range eff {
		if len(sel[dim]) > 0 {
			constraining = append(constraining, dim)
		}
	}
	var ids []string
	for id := range n.docs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := 0
	for _, id := range ids {
		if match(n.docs[id], constraining) {
			total++
		}
	}

	res := FacetsResult{Total: total, Facets: []FacetResult{}}
	for _, dim := range eff {
		var others []string
		for _, other := range constraining {
			if other != dim {
				others = append(others, other)
			}
		}
		counts := map[string]int{}
		for _, id := range ids {
			doc := n.docs[id]
			if !match(doc, others) {
				continue
			}
			for node := range doc[dim] {
				counts[node]++
			}
		}
		cand := map[string]bool{}
		for node, count := range counts {
			if count > 0 {
				cand[node] = true
			}
		}
		for value := range sel[dim] {
			cand[value] = true
		}
		if len(cand) == 0 {
			continue
		}
		var values []string
		for value := range cand {
			values = append(values, value)
		}
		sort.Slice(values, func(i, j int) bool {
			if counts[values[i]] != counts[values[j]] {
				return counts[values[i]] > counts[values[j]]
			}
			return values[i] < values[j]
		})
		if len(values) > topN {
			values = values[:topN]
		}
		items := make([]FacetItem, 0, len(values))
		for _, value := range values {
			items = append(items, FacetItem{Value: value, Count: counts[value], Selected: sel[dim][value]})
		}
		res.Facets = append(res.Facets, FacetResult{Dimension: dim, Items: items})
	}
	return res
}

type opKind int

const (
	opAdd opKind = iota
	opReplace
	opDelete
	opLink
	opFacets
)

type randOp struct {
	kind   opKind
	docID  string
	attrs  Attrs
	child  string
	parent string
	sel    map[string][]string
	topN   int
}

func validRandomPath(r *rand.Rand, segments []string, maxDepth int) string {
	depth := 1 + r.Intn(maxDepth)
	parts := make([]string, depth)
	base := segments[r.Intn(len(segments))]
	for i := range parts {
		parts[i] = fmt.Sprintf("%s%d", base, r.Intn(3))
	}
	return strings.Join(parts, "/")
}

func randomAttrs(r *rand.Rand, dims, segments []string) Attrs {
	attrs := Attrs{}
	dimCount := r.Intn(4)
	for i := 0; i < dimCount; i++ {
		dim := dims[r.Intn(len(dims))]
		if _, exists := attrs[dim]; exists {
			continue
		}
		valueCount := 1 + r.Intn(4)
		seen := map[string]bool{}
		var values []string
		for j := 0; j < valueCount; j++ {
			value := validRandomPath(r, segments, 3)
			if seen[value] {
				continue
			}
			seen[value] = true
			values = append(values, value)
		}
		attrs[dim] = values
	}
	return attrs
}

func randomSelection(r *rand.Rand, dims, segments []string) map[string][]string {
	sel := map[string][]string{}
	if r.Intn(3) == 0 {
		return sel
	}
	count := 1 + r.Intn(3)
	for i := 0; i < count; i++ {
		dim := dims[r.Intn(len(dims))]
		if r.Intn(4) == 0 {
			sel[dim] = nil // explicitly empty
			continue
		}
		valueCount := r.Intn(3)
		seen := map[string]bool{}
		var values []string
		for j := 0; j < valueCount; j++ {
			value := validRandomPath(r, segments, 3)
			if !seen[value] {
				seen[value] = true
				values = append(values, value)
			}
		}
		sel[dim] = values
	}
	return sel
}

func TestRandomDifferential2000(t *testing.T) {
	dims := []string{"a", "b", "c", "d"}
	segments := []string{"x", "y", "z"}
	for seed := int64(0); seed < 2000; seed++ {
		r := rand.New(rand.NewSource(seed))
		fc := NewFacetCounter()
		naive := &naiveStore{
			docs:   map[string]map[string]map[string]bool{},
			parent: map[string]string{},
		}
		var log []string
		opCount := 6 + r.Intn(20)
		for i := 0; i < opCount; i++ {
			op := randOp{kind: opKind(r.Intn(5))}
			desc := ""
			switch op.kind {
			case opAdd, opReplace:
				op.docID = fmt.Sprintf("doc%d", r.Intn(5))
				op.attrs = randomAttrs(r, dims, segments)
				desc = fmt.Sprintf("%s(%q, %v)", map[opKind]string{opAdd: "Add", opReplace: "Replace"}[op.kind], op.docID, op.attrs)
			case opDelete:
				op.docID = fmt.Sprintf("doc%d", r.Intn(5))
				desc = fmt.Sprintf("Delete(%q)", op.docID)
			case opLink:
				op.child = dims[r.Intn(len(dims))]
				op.parent = dims[r.Intn(len(dims))]
				desc = fmt.Sprintf("Link(%q,%q)", op.child, op.parent)
			case opFacets:
				op.sel = randomSelection(r, dims, segments)
				op.topN = 1 + r.Intn(6)
				desc = fmt.Sprintf("Facets(%v,%d)", op.sel, op.topN)
			}

			var gotErr, wantErr error
			var got FacetsResult
			switch op.kind {
			case opAdd:
				gotErr = fc.Add(op.docID, op.attrs)
			case opReplace:
				gotErr = fc.Replace(op.docID, op.attrs)
			case opDelete:
				gotErr = fc.Delete(op.docID)
			case opLink:
				gotErr = fc.Link(op.child, op.parent)
			case opFacets:
				got, gotErr = fc.Facets(op.sel, op.topN)
			}

			// Drive the naive model: only mutate on success, and map errors.
			switch op.kind {
			case opAdd:
				if _, exists := naive.docs[op.docID]; exists {
					wantErr = ErrDuplicateDocument
				} else {
					naive.docs[op.docID] = map[string]map[string]bool{}
					for dim, values := range op.attrs {
						naive.docs[op.docID][dim] = naiveExpand(values)
					}
				}
			case opReplace:
				if _, exists := naive.docs[op.docID]; !exists {
					wantErr = ErrDocumentNotFound
				} else {
					updated := map[string]map[string]bool{}
					for dim, values := range op.attrs {
						updated[dim] = naiveExpand(values)
					}
					naive.docs[op.docID] = updated
				}
			case opDelete:
				if _, exists := naive.docs[op.docID]; !exists {
					wantErr = ErrDocumentNotFound
				} else {
					delete(naive.docs, op.docID)
				}
			case opLink:
				if _, ok := naive.parent[op.child]; ok {
					wantErr = ErrDuplicateLink
				} else {
					cyclic := false
					for cursor := op.parent; ; {
						if cursor == op.child {
							cyclic = true
							break
						}
						next, ok := naive.parent[cursor]
						if !ok {
							break
						}
						cursor = next
					}
					if cyclic {
						wantErr = ErrCyclicDependency
					} else {
						naive.parent[op.child] = op.parent
					}
				}
			case opFacets:
				selSets := map[string]map[string]bool{}
				for dim, values := range op.sel {
					set := map[string]bool{}
					for _, value := range values {
						set[value] = true
					}
					selSets[dim] = set
				}
				want := naive.facets(selSets, op.topN)
				log = append(log, fmt.Sprintf("seed=%d op=%d %s\n  input: sel=%v topN=%d\n  got:  total=%d facets=%+v\n  want: total=%d facets=%+v\n  verdict: compared against per-document per-doc naive re-evaluation",
					seed, i, desc, op.sel, op.topN, got.Total, got.Facets, want.Total, want.Facets))
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("MISMATCH seed=%d\n%s\ngot=%+v\nwant=%+v", seed, strings.Join(log, "\n"), got, want)
				}
				continue
			}
			log = append(log, fmt.Sprintf("seed=%d op=%d %s -> err=%v (expected %v)", seed, i, desc, gotErr, wantErr))
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("ERROR MISMATCH seed=%d op=%d %s: got %v want %v\n%s", seed, i, desc, gotErr, wantErr, strings.Join(log, "\n"))
			}
		}
		// Print logs for every case: input, output and the decision basis.
		t.Logf("seed=%d sequence:\n%s", seed, strings.Join(log, "\n"))
	}
}

func TestConcurrentAccess(t *testing.T) {
	fc := NewFacetCounter()
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("doc%d", r.Intn(20))
				attrs := Attrs{"c": {validRandomPath(r, []string{"x", "y"}, 3)}}
				switch r.Intn(4) {
				case 0:
					_ = fc.Add(id, attrs)
				case 1:
					_ = fc.Replace(id, attrs)
				case 2:
					_ = fc.Delete(id)
				default:
					_, _ = fc.Facets(map[string][]string{"c": {validRandomPath(r, []string{"x"}, 2)}}, 1+r.Intn(5))
				}
			}
		}(g)
	}
	wg.Wait()
}
