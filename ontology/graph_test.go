package ontology

import (
	"errors"
	"reflect"
	"testing"
)

const (
	tNode = TypeName("node")
	lEdge = TypeName("edge")
	lPair = TypeName("pair")
	alice = Principal("alice")
	bob   = Principal("bob")
)

func newTestGraph(t *testing.T) *Graph {
	t.Helper()
	g := NewGraph()
	if err := g.AddObjectType(ObjectType{Name: tNode}); err != nil {
		t.Fatalf("add object type: %v", err)
	}
	if err := g.AddLinkType(LinkType{Name: lEdge, Source: tNode, Sink: tNode, Direct: Directed}); err != nil {
		t.Fatalf("add link type edge: %v", err)
	}
	if err := g.AddLinkType(LinkType{Name: lPair, Source: tNode, Sink: tNode, Direct: Undirected}); err != nil {
		t.Fatalf("add link type pair: %v", err)
	}
	return g
}

func mustAddObject(t *testing.T, g *Graph, id ObjectID, readers ...Principal) {
	t.Helper()
	if err := g.AddObject(Object{ID: id, Type: tNode, Readers: readers}); err != nil {
		t.Fatalf("add object %s: %v", id, err)
	}
}

func mustAddLink(t *testing.T, g *Graph, typ TypeName, a, b ObjectID) {
	t.Helper()
	if err := g.AddLink(Link{Type: typ, Source: a, Sink: b}); err != nil {
		t.Fatalf("add link %s %s->%s: %v", typ, a, b, err)
	}
}

func objectIDs(objs []Object) []ObjectID {
	out := make([]ObjectID, len(objs))
	for i, o := range objs {
		out[i] = o.ID
	}
	return out
}

// TestExtractInvalidArguments covers the decision order: parameter errors are
// reported before any permission or dangling logic.
func TestExtractInvalidArguments(t *testing.T) {
	g := newTestGraph(t)

	if _, err := g.Extract(alice, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty scope: want ErrInvalidArgument, got %v", err)
	}
	if _, err := g.Extract(alice, []ObjectID{""}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty id: want ErrInvalidArgument, got %v", err)
	}
	if _, err := g.Extract(alice, []ObjectID{"bad id"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("space in id: want ErrInvalidArgument, got %v", err)
	}

	big := make([]ObjectID, MaxScopeSize+1)
	for i := range big {
		big[i] = ObjectID("x" + itoa(i))
	}
	if _, err := g.Extract(alice, big); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("oversize scope: want ErrInvalidArgument, got %v", err)
	}
}

// TestExtractDanglingSources distinguishes boundary vs permission dangling
// links, including redaction and the scope-over-permission precedence.
func TestExtractDanglingSources(t *testing.T) {
	g := newTestGraph(t)
	// A visible, B in scope but invisible to alice (permission), C exists but
	// never requested (boundary).
	mustAddObject(t, g, "A", alice)
	mustAddObject(t, g, "B", bob)
	mustAddObject(t, g, "C", alice)
	// D in scope but invisible; linked to C which is out of the requested
	// scope: from any included end this link is never seen.
	mustAddObject(t, g, "D", bob)
	// E is outside the scope, invisible to alice, and linked from A: a
	// boundary link whose remote existence must be redacted. This is the
	// precedence case (out of scope AND unreadable).
	mustAddObject(t, g, "E", bob)

	mustAddLink(t, g, lEdge, "A", "B") // permission dangling from A
	mustAddLink(t, g, lEdge, "A", "C") // boundary dangling from A
	mustAddLink(t, g, lEdge, "C", "D") // both ends excluded: nothing
	mustAddLink(t, g, lEdge, "A", "E") // boundary dangling, remote redacted

	snap, err := g.Extract(alice, []ObjectID{"A", "B", "D"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}

	if got := objectIDs(snap.Objects); !reflect.DeepEqual(got, []ObjectID{"A"}) {
		t.Fatalf("included objects = %v, want [A]", got)
	}
	if len(snap.Links) != 0 {
		t.Fatalf("internal links = %v, want none", snap.Links)
	}

	if len(snap.Dangling) != 3 {
		t.Fatalf("dangling count = %d (%+v), want 3", len(snap.Dangling), snap.Dangling)
	}

	byType := map[ObjectID]DanglingLink{}
	var redacted []DanglingLink
	for _, d := range snap.Dangling {
		if d.LocalEnd != "A" {
			t.Fatalf("unexpected local end %q", d.LocalEnd)
		}
		if d.RemoteRedacted {
			redacted = append(redacted, d)
		} else {
			byType[d.RemoteEnd] = d
		}
	}
	if d, ok := byType["C"]; !ok || d.Source != DanglingByScope || d.RemoteRedacted {
		t.Fatalf("A->C record wrong: %+v ok=%v", d, ok)
	}
	if len(redacted) != 2 {
		t.Fatalf("redacted records = %+v, want 2 (B permission, E scope)", redacted)
	}
	srcs := map[DanglingSource]bool{}
	for _, d := range redacted {
		srcs[d.Source] = true
	}
	if !srcs[DanglingByScope] || !srcs[DanglingByPermission] {
		t.Fatalf("redacted sources = %v, want both scope and permission", srcs)
	}
}

// TestExtractStrippedToEmpty verifies that stripping removes every link and
// yields a successful empty snapshot (not an error).
func TestExtractStrippedToEmpty(t *testing.T) {
	g := newTestGraph(t)
	mustAddObject(t, g, "A", bob)
	mustAddObject(t, g, "B", bob)
	mustAddLink(t, g, lEdge, "A", "B")

	snap, err := g.Extract(alice, []ObjectID{"A", "B"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(snap.Objects) != 0 || len(snap.Links) != 0 || len(snap.Dangling) != 0 {
		t.Fatalf("want empty snapshot, got %+v", snap)
	}
}

// TestExtractSelfLoop checks self-loop edge semantics: a self loop on an
// included object is an internal link; it never participates in dangling
// classification. A self loop on a removed object vanishes entirely.
func TestExtractSelfLoop(t *testing.T) {
	g := newTestGraph(t)
	mustAddObject(t, g, "A", alice)
	mustAddObject(t, g, "B", bob)
	mustAddLink(t, g, lEdge, "A", "A")
	mustAddLink(t, g, lEdge, "B", "B")

	snap, err := g.Extract(alice, []ObjectID{"A", "B"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(snap.Objects) != 1 || snap.Objects[0].ID != "A" {
		t.Fatalf("objects = %v, want [A]", objectIDs(snap.Objects))
	}
	if len(snap.Links) != 1 || snap.Links[0].Source != "A" || snap.Links[0].Sink != "A" {
		t.Fatalf("links = %v, want single A->A self loop", snap.Links)
	}
	if len(snap.Dangling) != 0 {
		t.Fatalf("dangling = %+v, want none (self loops never dangle)", snap.Dangling)
	}
}

// TestExtractRepeatabilityAndAttribution checks that two identical extractions
// with no intervening change are byte-for-byte equal, and that after a change
// every difference is explained by journaled changes at the boundary epoch.
func TestExtractRepeatabilityAndAttribution(t *testing.T) {
	g := newTestGraph(t)
	mustAddObject(t, g, "A", alice)
	mustAddObject(t, g, "B", alice)
	mustAddObject(t, g, "C", alice)
	mustAddLink(t, g, lEdge, "A", "B")
	mustAddLink(t, g, lEdge, "A", "C")

	s1, err := g.Extract(alice, []ObjectID{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	s2, err := g.Extract(alice, []ObjectID{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if s1.Epoch != s2.Epoch || !reflect.DeepEqual(s1, s2) {
		t.Fatalf("repeat extraction differs without changes:\n%+v\n%+v", s1, s2)
	}
	if s1.Dangling[0].LocalEnd != "A" || s1.Dangling[0].RemoteEnd != "C" {
		t.Fatalf("dangling order/content unstable: %+v", s1.Dangling)
	}

	epochBefore := g.Epoch()
	// Change inside the window: grant effectively nothing; instead remove C
	// and add a new internal link A->B via pair type — actually add D.
	mustAddObject(t, g, "D", alice)
	mustAddLink(t, g, lEdge, "B", "D")
	changes := g.Journal()

	s3, err := g.Extract(alice, []ObjectID{"A", "B"})
	if err != nil {
		t.Fatal(err)
	}
	if s3.Epoch <= s1.Epoch {
		t.Fatalf("epoch did not advance: %d -> %d", s1.Epoch, s3.Epoch)
	}
	// Every journaled change between epochs must touch only ids that explain
	// the diff window.
	window := []Change{}
	for _, c := range changes {
		if c.Epoch > epochBefore && c.Epoch <= s3.Epoch {
			window = append(window, c)
		}
	}
	if len(window) != 2 {
		t.Fatalf("window changes = %d, want 2 (object+link)", len(window))
	}
	// s1 and s3 bodies over the old scope are identical: the new object D is
	// out of scope, so the only new fact is a dangling record B->D.
	if !reflect.DeepEqual(s1.Objects, s3.Objects) || !reflect.DeepEqual(s1.Links, s3.Links) {
		t.Fatalf("scope-contained body changed unexpectedly")
	}
	found := false
	for _, d := range s3.Dangling {
		if d.LocalEnd == "B" && d.RemoteEnd == "D" && d.Source == DanglingByScope {
			found = true
		}
	}
	if !found {
		t.Fatalf("new dangling B->D not attributable: %+v", s3.Dangling)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [24]byte
	n := len(b)
	for i > 0 {
		n--
		b[n] = byte('0' + i%10)
		i /= 10
	}
	return string(b[n:])
}
