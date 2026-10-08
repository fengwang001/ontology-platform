package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore([]Category{"a", "b", "c"}, []ObjectType{"t0", "t1"})
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func addTypes(t *testing.T, s *Store, lts ...LinkType) {
	t.Helper()
	for _, lt := range lts {
		if err := s.AddLinkType(lt); err != nil {
			t.Fatalf("AddLinkType %q: %v", lt.ID, err)
		}
	}
}

func addObjs(t *testing.T, s *Store, ids ...ObjectID) {
	t.Helper()
	for _, id := range ids {
		if err := s.AddObject(id, "t0"); err != nil {
			t.Fatalf("AddObject %q: %v", id, err)
		}
	}
}

func addLinks(t *testing.T, s *Store, links ...[4]string) {
	t.Helper()
	for _, l := range links {
		if err := s.AddLink(LinkID(l[0]), LinkTypeID(l[1]), ObjectID(l[2]), ObjectID(l[3])); err != nil {
			t.Fatalf("AddLink %q: %v", l[0], err)
		}
	}
}

func anyPos() ConstraintPos            { return ConstraintPos{Any: true} }
func anyStar() ConstraintPos           { return ConstraintPos{Any: true, Star: true} }
func catPos(c Category) ConstraintPos  { return ConstraintPos{Category: c} }
func catStar(c Category) ConstraintPos { return ConstraintPos{Category: c, Star: true} }

// baseTypes registers directed t0->t0 link types: LA/LB cost 1, LA5/LB5
// cost 5, plus bidirectional LBI cost 1.
func baseTypes() []LinkType {
	return []LinkType{
		{ID: "LA", SrcType: "t0", DstType: "t0", Category: "a", Cost: 1},
		{ID: "LB", SrcType: "t0", DstType: "t0", Category: "b", Cost: 1},
		{ID: "LC", SrcType: "t0", DstType: "t0", Category: "c", Cost: 1},
		{ID: "LA5", SrcType: "t0", DstType: "t0", Category: "a", Cost: 5},
		{ID: "LB5", SrcType: "t0", DstType: "t0", Category: "b", Cost: 5},
		{ID: "LBI", SrcType: "t0", DstType: "t0", Category: "b", Cost: 1, Bidirectional: true},
	}
}

// chainStore builds s -a-> x -a-> m -b-> e (all cost 1).
func chainStore(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "x", "m", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "x"},
		[4]string{"l2", "LA", "x", "m"},
		[4]string{"l3", "LB", "m", "e"},
	)
	return s
}

func queryOne(t *testing.T, s *Store, start, end ObjectID, cons ...ConstraintPos) (Result, error) {
	t.Helper()
	return s.Query(Query{Start: start, End: end, Principal: "root", Constraint: cons})
}

func TestConstraintMarkers(t *testing.T) {
	s := chainStore(t)
	cases := []struct {
		name      string
		start     ObjectID
		end       ObjectID
		cons      []ConstraintPos
		wantFound bool
		wantObjs  []ObjectID
	}{
		{"exact", "s", "e", []ConstraintPos{catPos("a"), catPos("a"), catPos("b")}, true, []ObjectID{"s", "x", "m", "e"}},
		{"too short", "s", "e", []ConstraintPos{catPos("a"), catPos("b")}, false, nil},
		{"too long", "s", "e", []ConstraintPos{catPos("a"), catPos("a"), catPos("b"), catPos("b")}, false, nil},
		{"wrong category", "s", "e", []ConstraintPos{catPos("a"), catPos("b"), catPos("b")}, false, nil},
		{"wildcards", "s", "e", []ConstraintPos{anyPos(), anyPos(), anyPos()}, true, []ObjectID{"s", "x", "m", "e"}},
		{"front star absorbs", "s", "e", []ConstraintPos{catStar("a"), catPos("b")}, true, []ObjectID{"s", "x", "m", "e"}},
		{"front star zero reps", "m", "e", []ConstraintPos{catStar("a"), catPos("b")}, true, []ObjectID{"m", "e"}},
		{"front star wrong tail", "s", "e", []ConstraintPos{catStar("b"), catPos("a")}, false, nil},
		{"back star absorbs", "s", "e", []ConstraintPos{catPos("a"), catPos("a"), catStar("b")}, true, []ObjectID{"s", "x", "m", "e"}},
		{"back star zero reps", "s", "x", []ConstraintPos{catPos("a"), catStar("b")}, true, []ObjectID{"s", "x"}},
		{"both stars", "s", "e", []ConstraintPos{catStar("a"), catStar("b")}, true, []ObjectID{"s", "x", "m", "e"}},
		{"wildcard star front", "s", "e", []ConstraintPos{anyStar(), catPos("b")}, true, []ObjectID{"s", "x", "m", "e"}},
		{"single star empty", "s", "s", []ConstraintPos{catStar("a")}, true, []ObjectID{"s"}},
		{"single star nonempty", "s", "x", []ConstraintPos{catStar("a")}, true, []ObjectID{"s", "x"}},
		{"single no star start==end", "s", "s", []ConstraintPos{catPos("a")}, false, nil},
		{"both stars empty", "s", "s", []ConstraintPos{catStar("a"), catStar("b")}, true, []ObjectID{"s"}},
		{"front only star empty", "s", "s", []ConstraintPos{catPos("a"), catStar("b")}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := queryOne(t, s, tc.start, tc.end, tc.cons...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Found != tc.wantFound {
				t.Fatalf("Found=%v, want %v (res=%+v)", res.Found, tc.wantFound, res)
			}
			if tc.wantFound && !reflect.DeepEqual(res.Path.Objects, tc.wantObjs) {
				t.Fatalf("objects=%v, want %v", res.Path.Objects, tc.wantObjs)
			}
		})
	}
}

// TestTieBreakCategoryOrder: two equal-cost paths, the one with the
// lexicographically smaller category sequence (fixed order a<b<c) wins.
func TestTieBreakCategoryOrder(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "p", "q", "e")
	addLinks(t, s,
		[4]string{"l1", "LB", "s", "p"}, // s -b-> p -a-> e : cats [b,a]
		[4]string{"l2", "LA", "p", "e"},
		[4]string{"l3", "LA", "s", "q"}, // s -a-> q -b-> e : cats [a,b]
		[4]string{"l4", "LB", "q", "e"},
	)
	res, err := queryOne(t, s, "s", "e", anyPos(), anyPos())
	if err != nil || !res.Found {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := res.Path.Objects; !reflect.DeepEqual(got, []ObjectID{"s", "q", "e"}) {
		t.Fatalf("objects=%v, want [s q e] (category sequence [a b] < [b a])", got)
	}
	if res.Equivalent {
		t.Fatalf("unexpected equivalence")
	}
}

// TestTieBreakObjectOrder: equal cost and equal category sequences, the
// lexicographically smaller object ID sequence wins, regardless of the
// order links were inserted.
func TestTieBreakObjectOrder(t *testing.T) {
	build := func(reverse bool) *Store {
		s := newTestStore(t)
		addTypes(t, s, baseTypes()...)
		addObjs(t, s, "s", "m1", "m2", "e")
		links := [][4]string{
			{"l1", "LA", "s", "m1"},
			{"l2", "LB", "m1", "e"},
			{"l3", "LA", "s", "m2"},
			{"l4", "LB", "m2", "e"},
		}
		if reverse {
			for i, j := 0, len(links)-1; i < j; i, j = i+1, j-1 {
				links[i], links[j] = links[j], links[i]
			}
		}
		addLinks(t, s, links...)
		return s
	}
	for _, rev := range []bool{false, true} {
		res, err := queryOne(t, build(rev), "s", "e", catPos("a"), catPos("b"))
		if err != nil || !res.Found {
			t.Fatalf("rev=%v res=%+v err=%v", rev, res, err)
		}
		if got := res.Path.Objects; !reflect.DeepEqual(got, []ObjectID{"s", "m1", "e"}) {
			t.Fatalf("rev=%v objects=%v, want [s m1 e]", rev, got)
		}
	}
}

// TestEquivalentPaths: parallel links of the same type produce distinct
// paths that tie on cost, categories and objects; the result must flag
// the equivalence and deterministically pick the smallest link ID
// sequence instead of a random one.
func TestEquivalentPaths(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "x", "e")
	addLinks(t, s,
		[4]string{"l2", "LA", "s", "x"}, // inserted out of order on purpose
		[4]string{"l1", "LA", "s", "x"},
		[4]string{"l3", "LB", "x", "e"},
	)
	res, err := queryOne(t, s, "s", "e", catPos("a"), catPos("b"))
	if err != nil || !res.Found {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !res.Equivalent {
		t.Fatalf("expected equivalence to be reported")
	}
	if got := res.Path.Links; !reflect.DeepEqual(got, []LinkID{"l1", "l3"}) {
		t.Fatalf("links=%v, want canonical [l1 l3]", got)
	}
	// Determinism: repeated queries return identical results.
	for i := 0; i < 20; i++ {
		again, err := queryOne(t, s, "s", "e", catPos("a"), catPos("b"))
		if err != nil || !reflect.DeepEqual(again, res) {
			t.Fatalf("run %d differs: %+v vs %+v", i, again, res)
		}
	}
}

// TestIsolation: an isolated object may not be an intermediate node but
// may still be the start or the end of a direct query.
func TestIsolation(t *testing.T) {
	build := func() *Store {
		s := newTestStore(t)
		addTypes(t, s, baseTypes()...)
		addObjs(t, s, "s", "m", "n", "e")
		addLinks(t, s,
			[4]string{"l1", "LA", "s", "m"}, // cheap path via m: cost 2
			[4]string{"l2", "LB", "m", "e"},
			[4]string{"l3", "LA5", "s", "n"}, // expensive path via n: cost 10
			[4]string{"l4", "LB5", "n", "e"},
		)
		return s
	}
	cons := []ConstraintPos{catPos("a"), catPos("b")}

	s := build()
	res, err := queryOne(t, s, "s", "e", cons...)
	if err != nil || !res.Found || res.Path.TotalCost != 2 {
		t.Fatalf("baseline: res=%+v err=%v", res, err)
	}

	// Isolated intermediate node forces a reroute.
	s = build()
	if err := s.SetIsolation("m", true); err != nil {
		t.Fatal(err)
	}
	res, err = queryOne(t, s, "s", "e", cons...)
	if err != nil || !res.Found || res.Path.TotalCost != 10 {
		t.Fatalf("isolated m: res=%+v err=%v", res, err)
	}
	if reflect.DeepEqual(res.Path.Objects, []ObjectID{"s", "m", "e"}) {
		t.Fatalf("isolated object used as intermediate node")
	}

	// All intermediates isolated: unreachable (a legal result, no error).
	if err := s.SetIsolation("n", true); err != nil {
		t.Fatal(err)
	}
	res, err = queryOne(t, s, "s", "e", cons...)
	if err != nil || res.Found {
		t.Fatalf("all isolated: res=%+v err=%v", res, err)
	}

	// Isolated end object is still directly reachable.
	s = build()
	if err := s.SetIsolation("e", true); err != nil {
		t.Fatal(err)
	}
	res, err = queryOne(t, s, "s", "e", cons...)
	if err != nil || !res.Found || res.Path.TotalCost != 2 {
		t.Fatalf("isolated end: res=%+v err=%v", res, err)
	}

	// Isolated start object still works as a path endpoint.
	s = build()
	if err := s.SetIsolation("s", true); err != nil {
		t.Fatal(err)
	}
	res, err = queryOne(t, s, "s", "e", cons...)
	if err != nil || !res.Found || res.Path.TotalCost != 2 {
		t.Fatalf("isolated start: res=%+v err=%v", res, err)
	}
}

// TestPermissionVisibility: links of a restricted type are nonexistent
// for a principal without permission, forcing a reroute or unreachability.
func TestPermissionVisibility(t *testing.T) {
	build := func() *Store {
		s := newTestStore(t)
		lts := append(baseTypes(), LinkType{
			ID: "LH", SrcType: "t0", DstType: "t0", Category: "a", Cost: 1,
			Principals: map[Principal]bool{"admin": true},
		})
		addTypes(t, s, lts...)
		addObjs(t, s, "s", "m", "n", "e")
		addLinks(t, s,
			[4]string{"l1", "LH", "s", "m"}, // hidden cheap path: cost 2
			[4]string{"l2", "LB", "m", "e"},
			[4]string{"l3", "LB5", "s", "n"}, // public expensive path: cost 10
			[4]string{"l4", "LB5", "n", "e"},
		)
		return s
	}
	cons := []ConstraintPos{anyPos(), anyPos()}

	s := build()
	res, err := s.Query(Query{Start: "s", End: "e", Principal: "admin", Constraint: cons})
	if err != nil || !res.Found || res.Path.TotalCost != 2 {
		t.Fatalf("admin: res=%+v err=%v", res, err)
	}

	res, err = s.Query(Query{Start: "s", End: "e", Principal: "guest", Constraint: cons})
	if err != nil || !res.Found || res.Path.TotalCost != 10 {
		t.Fatalf("guest reroute: res=%+v err=%v", res, err)
	}
	for _, l := range res.Path.Links {
		if l == "l1" {
			t.Fatalf("guest path uses invisible link l1")
		}
	}

	// Without the public alternative the hidden path must be
	// indistinguishable from nonexistence: plain unreachable.
	s = build()
	if err := s.RemoveLink("l3"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveLink("l4"); err != nil {
		t.Fatal(err)
	}
	res, err = s.Query(Query{Start: "s", End: "e", Principal: "guest", Constraint: cons})
	if err != nil || res.Found {
		t.Fatalf("guest unreachable: res=%+v err=%v", res, err)
	}
	res, err = s.Query(Query{Start: "s", End: "e", Principal: "admin", Constraint: cons})
	if err != nil || !res.Found {
		t.Fatalf("admin still sees path: res=%+v err=%v", res, err)
	}
}

// TestErrorPrecedence pins the decision order: invalid parameters, then
// forbidden object types, then plain unreachability (a legal result).
func TestErrorPrecedence(t *testing.T) {
	s := chainStore(t)

	invalid := []struct {
		name string
		q    Query
	}{
		{"empty constraint", Query{Start: "s", End: "e"}},
		{"middle star", Query{Start: "s", End: "e", Constraint: []ConstraintPos{catPos("a"), catStar("b"), catPos("c")}}},
		{"unknown category", Query{Start: "s", End: "e", Constraint: []ConstraintPos{catPos("zz")}}},
		{"missing start", Query{Start: "nope", End: "e", Constraint: []ConstraintPos{anyPos()}}},
		{"missing end", Query{Start: "s", End: "nope", Constraint: []ConstraintPos{anyPos()}}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Query(tc.q); !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("err=%v, want ErrInvalidParams", err)
			}
		})
	}

	// Forbidden type of start or end object.
	if err := s.SetTypeForbidden("t0", true); err != nil {
		t.Fatal(err)
	}
	if _, err := queryOne(t, s, "s", "e", anyPos()); !errors.Is(err, ErrForbiddenType) {
		t.Fatalf("forbidden: err=%v, want ErrForbiddenType", err)
	}
	// Invalid parameters win over forbidden types.
	if _, err := s.Query(Query{Start: "s", End: "e"}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("precedence: err=%v, want ErrInvalidParams", err)
	}
	if err := s.SetTypeForbidden("t0", false); err != nil {
		t.Fatal(err)
	}

	// Unreachable is a legal result, not an error.
	res, err := queryOne(t, s, "e", "s", anyPos())
	if err != nil || res.Found {
		t.Fatalf("unreachable: res=%+v err=%v", res, err)
	}
}

// TestDirection: directed links traverse only forward, bidirectional
// links traverse both ways.
func TestDirection(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "m", "u", "v")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "m"},
		[4]string{"l2", "LBI", "u", "v"},
	)
	if res, err := queryOne(t, s, "m", "s", catPos("a")); err != nil || res.Found {
		t.Fatalf("directed backward: res=%+v err=%v", res, err)
	}
	for _, q := range [][2]ObjectID{{"u", "v"}, {"v", "u"}} {
		res, err := queryOne(t, s, q[0], q[1], catPos("b"))
		if err != nil || !res.Found {
			t.Fatalf("bidirectional %v->%v: res=%+v err=%v", q[0], q[1], res, err)
		}
	}
}

// TestDuplicateLinksCountedIndependently: two links of the same type
// between the same pair are independent paths with independent costs.
func TestDuplicateLinksCountedIndependently(t *testing.T) {
	s := newTestStore(t)
	addTypes(t, s, baseTypes()...)
	addObjs(t, s, "s", "e")
	addLinks(t, s,
		[4]string{"l1", "LA", "s", "e"},
		[4]string{"l2", "LA5", "s", "e"},
	)
	res, err := queryOne(t, s, "s", "e", catPos("a"))
	if err != nil || !res.Found {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if res.Path.TotalCost != 1 || !reflect.DeepEqual(res.Path.Links, []LinkID{"l1"}) {
		t.Fatalf("path=%+v, want cost 1 via l1", res.Path)
	}
	if res.Equivalent {
		t.Fatalf("different costs must not be equivalent")
	}
}
