package ontology

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

// TestParameterValidationOrder: parameter rejection precedes all semantic
// processing, in the fixed order empty > malformed > oversize.
func TestParameterValidationOrder(t *testing.T) {
	g := NewGraph()
	ex := NewExtractor(g).WithMaxScope(3)

	if _, err := ex.Extract("p", nil); !errors.Is(err, ErrEmptyScope) {
		t.Fatalf("empty scope: got %v want ErrEmptyScope", err)
	}
	bad := []ID{"bad id!", "a", "b", "c"}
	if _, err := ex.Extract("p", bad); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("malformed: got %v want ErrInvalidID", err)
	}
	if _, err := ex.Extract("p", []ID{"a", "b", "c", "d"}); !errors.Is(err, ErrScopeTooLarge) {
		t.Fatalf("oversize: got %v want ErrScopeTooLarge", err)
	}
}

// TestBoundaryVsPermissionDangling distinguishes the two sources and
// checks boundary-first precedence, direction metadata and exclusion.
func TestBoundaryVsPermissionDangling(t *testing.T) {
	g := NewGraph()
	for _, id := range []ID{"a", "b", "c", "d"} {
		mustObj(t, g, id)
	}
	mustLink(t, g, "l_ab", dirT, "a", "b")
	mustLink(t, g, "l_bc", dirT, "c", "b")
	mustLink(t, g, "l_bd", undT, "b", "d")
	grant(t, g, "alice", "a", "b")

	snap, err := NewExtractor(g).Extract("alice", []ID{"a", "b", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if got := linkIDs(snap); !reflect.DeepEqual(got, []ID{"l_ab"}) {
		t.Fatalf("included links = %v, want [l_ab]", got)
	}
	if got := danglingIDs(snap); !reflect.DeepEqual(got, []ID{"l_bc", "l_bd"}) {
		t.Fatalf("dangling order = %v, want [l_bc l_bd]", got)
	}
	recBC, ok := snap.DanglingByLink("l_bc")
	if !ok || recBC.Source != DanglingBoundary {
		t.Fatalf("l_bc source = %v, want boundary", recBC.Source)
	}
	if recBC.Included != "b" || recBC.Excluded != "c" || recBC.Dir != DirIn {
		t.Fatalf("l_bc record wrong: %+v", recBC)
	}
	recBD, ok := snap.DanglingByLink("l_bd")
	if !ok || recBD.Source != DanglingPermission {
		t.Fatalf("l_bd source = %v, want permission", recBD.Source)
	}
	if recBD.Included != "b" || recBD.Excluded != "d" || recBD.Dir != DirEither {
		t.Fatalf("l_bd record wrong: %+v", recBD)
	}

	objSet := map[ID]bool{}
	for _, o := range snap.Objects {
		objSet[o.ID] = true
	}
	for _, l := range snap.Links {
		if !objSet[l.From] || !objSet[l.To] {
			t.Fatalf("snapshot link %s missing endpoint", l.ID)
		}
	}
}

// TestBoundaryFirstPrecedence: an endpoint never requested and without a
// grant must be labeled boundary, even though it also fails visibility.
func TestBoundaryFirstPrecedence(t *testing.T) {
	g := NewGraph()
	mustObj(t, g, "a")
	mustObj(t, g, "z")
	mustLink(t, g, "l_az", dirT, "a", "z")
	grant(t, g, "p", "a")
	snap, err := NewExtractor(g).Extract("p", []ID{"a"})
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := snap.DanglingByLink("l_az")
	if !ok || rec.Source != DanglingBoundary {
		t.Fatalf("want boundary precedence, got %+v ok=%v", rec, ok)
	}
}

// TestSelfLinkNeverDangling: self links are either fully included (object
// survives) or disappear with the removed object; never dangling.
func TestSelfLinkNeverDangling(t *testing.T) {
	g := NewGraph()
	mustObj(t, g, "a")
	mustObj(t, g, "x")
	mustLink(t, g, "self_a", dirT, "a", "a")
	mustLink(t, g, "self_x", dirT, "x", "x")
	grant(t, g, "p", "a")

	snap, err := NewExtractor(g).Extract("p", []ID{"a", "x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := linkIDs(snap); !reflect.DeepEqual(got, []ID{"self_a"}) {
		t.Fatalf("included = %v, want [self_a]", got)
	}
	if len(snap.Dangling) != 0 {
		t.Fatalf("self links must never dangle, got %+v", snap.Dangling)
	}
}

// TestScopeRemovedToEmpty: full removal yields an empty snapshot, no error.
func TestScopeRemovedToEmpty(t *testing.T) {
	g := NewGraph()
	mustObj(t, g, "a")
	mustObj(t, g, "b")
	mustLink(t, g, "l_ab", undT, "a", "b")
	snap, err := NewExtractor(g).Extract("stranger", []ID{"a", "b"})
	if err != nil {
		t.Fatalf("empty-after-removal must not error: %v", err)
	}
	if len(snap.Objects) != 0 || len(snap.Links) != 0 || len(snap.Dangling) != 0 {
		t.Fatalf("want fully empty snapshot, got %+v", snap)
	}
	if snap.Revision != g.Revision() {
		t.Fatalf("empty snapshot should still pin a revision")
	}
}

func findLink(s *Snapshot, id ID) (Link, bool) {
	for _, l := range s.Links {
		if l.ID == id {
			return l, true
		}
	}
	return Link{}, false
}

// TestRepeatedExtractionDeterministic: identical without changes; every
// difference after a change is attributable to that change; irrelevant
// far-away changes leave the snapshot content untouched.
func TestRepeatedExtractionDeterministic(t *testing.T) {
	g := NewGraph()
	for _, id := range []ID{"a", "b", "c"} {
		mustObj(t, g, id)
	}
	mustLink(t, g, "l_ab", dirT, "a", "b")
	mustLink(t, g, "l_bc", dirT, "b", "c")
	grant(t, g, "p", "a", "b", "c")
	ex := NewExtractor(g)
	scope := []ID{"c", "a", "b", "a"}

	s1, err := ex.Extract("p", scope)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := ex.Extract("p", scope)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(s1, s2) {
		t.Fatalf("repeated extraction differs without changes")
	}

	want, _ := findLink(s1, "l_bc")
	if _, err := g.RemoveLink("l_bc"); err != nil {
		t.Fatal(err)
	}
	s3, err := ex.Extract("p", scope)
	if err != nil {
		t.Fatal(err)
	}
	d := Compare(s1, s3)
	if !reflect.DeepEqual(d.LinksRemoved, []Link{want}) ||
		len(d.ObjectsAdded)+len(d.ObjectsRemoved)+len(d.LinksAdded)+
			len(d.DanglingAdded)+len(d.DanglingRemoved) != 0 {
		t.Fatalf("unexpected diff: %+v", d)
	}
	attr := g.Attribute(s1, s3)
	if len(attr.Changes) != 1 || attr.Changes[0].Kind != ChangeLinkRemoved ||
		attr.Changes[0].Link != "l_bc" {
		t.Fatalf("attribution = %+v, want one l_bc removal", attr)
	}

	mustObj(t, g, "far")
	s4, err := ex.Extract("p", scope)
	if err != nil {
		t.Fatal(err)
	}
	if !Compare(s3, s4).Empty() {
		t.Fatalf("irrelevant change must not alter snapshot")
	}
}

// TestCandidateMetricIsScopeBounded: the internal metric ignores unrelated
// objects and links and counts only inspected candidate links.
func TestCandidateMetricIsScopeBounded(t *testing.T) {
	g := NewGraph()
	mustObj(t, g, "a")
	mustObj(t, g, "b")
	mustLink(t, g, "l_ab", undT, "a", "b")
	grant(t, g, "p", "a", "b")

	baseline, err := NewExtractor(g).Extract("p", []ID{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	base := baseline.InternalCandidateLinksChecked()

	for i := 0; i < 200; i++ {
		id := ID("u" + itoa(i))
		mustObj(t, g, id)
		grant(t, g, "p", id)
	}
	for i := 0; i < 200; i++ {
		mustLink(t, g, "ul"+itoa(i), undT,
			ID("u"+itoa(i)), ID("u"+itoa((i+1)%200)))
	}
	mustObj(t, g, "w")
	mustLink(t, g, "l_aw", dirT, "a", "w")

	snap, err := NewExtractor(g).Extract("p", []ID{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.InternalCandidateLinksChecked(); got != base+1 {
		t.Fatalf("candidate metric = %d, want %d", got, base+1)
	}
	if got := linkIDs(snap); !reflect.DeepEqual(got, []ID{"l_ab"}) {
		t.Fatalf("unrelated component leaked: %v", got)
	}
	if got := danglingIDs(snap); !reflect.DeepEqual(got, []ID{"l_aw"}) {
		t.Fatalf("dangling = %v, want [l_aw]", got)
	}
}

// TestConcurrentExtractionAndMutation: under concurrent commits every
// observed snapshot is internally consistent; run under -race.
func TestConcurrentExtractionAndMutation(t *testing.T) {
	g := NewGraph()
	for i := 0; i < 30; i++ {
		id := ID("n" + itoa(i))
		mustObj(t, g, id)
		grant(t, g, "p", id)
	}
	for i := 0; i < 29; i++ {
		mustLink(t, g, "e"+itoa(i), undT,
			ID("n"+itoa(i)), ID("n"+itoa(i+1)))
	}
	ex := NewExtractor(g)
	scope := make([]ID, 30)
	for i := range scope {
		scope[i] = ID("n" + itoa(i))
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for k := 0; k < 100; k++ {
			id := ID("t" + itoa(k))
			if _, err := g.AddObject(Object{ID: id, Type: objT}); err != nil {
				t.Errorf("add %s: %v", id, err)
				return
			}
			if _, err := g.GrantExistence("p", id); err != nil {
				t.Errorf("grant %s: %v", id, err)
				return
			}
			if _, err := g.AddLink(Link{
				ID: ID("te" + itoa(k)), Type: undT, From: "n0", To: id,
			}); err != nil {
				t.Errorf("link %s: %v", id, err)
				return
			}
		}
		close(stop)
	}()
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap, err := ex.Extract("p", scope)
				if err != nil {
					t.Errorf("extract: %v", err)
					return
				}
				present := map[ID]bool{}
				for _, o := range snap.Objects {
					present[o.ID] = true
				}
				for _, l := range snap.Links {
					if !present[l.From] || !present[l.To] {
						t.Errorf("inconsistent snapshot at rev %d: link %s dangling",
							snap.Revision, l.ID)
						return
					}
				}
			}
		}
	}()
	wg.Wait()
}
