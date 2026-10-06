package narrowing

import "testing"

func mustAnalyze(t *testing.T, decls []Decl, body []Stmt) *Result {
	t.Helper()
	res, err := Analyze(decls, body)
	if err != nil {
		t.Fatalf("analysis failed: %v", err)
	}
	return res
}

func mustQuery(t *testing.T, res *Result, id StatementID, p Point, name string) Type {
	t.Helper()
	typ, reachable, ok := res.Query(id, p, name)
	if !ok {
		t.Fatalf("query %s/%s: unknown statement or variable", id, name)
	}
	if !reachable {
		t.Fatalf("query %s/%s: point unexpectedly unreachable", id, name)
	}
	return typ
}

func decl(name string, typ TypeExpr) Decl { return Decl{Name: name, Type: typ} }

// The false branch of a strict literal equality removes only the equal
// literal member and leaves atomic members untouched, while the true
// branch collapses atomics to the literal.
func TestLiteralEqualityAsymmetry(t *testing.T) {
	decls := []Decl{
		decl("x", UnionExpr(NumLitExpr(1), NumLitExpr(2), StrLitExpr("s"))),
		decl("y", NumberExpr()),
	}
	body := []Stmt{
		&If{ID: "if", Cond: EqLiteral{Var: "x", Lit: NumLit(1)},
			Then: []Stmt{&Return{ID: "r1"}}},
		&If{ID: "if2", Cond: EqLiteral{Var: "y", Lit: NumLit(7)},
			Then: []Stmt{&Return{ID: "r2"}}},
	}
	res := mustAnalyze(t, decls, body)

	// x === 1 false branch: literal 1 gone, 2 and "s" kept.
	got := mustQuery(t, res, "if", After, "x")
	want := UnionOf(NumberLiteral(2), StringLiteral("s"))
	if !got.Equal(want) {
		t.Fatalf("false branch of x===1: got %s, want %s", got, want)
	}
	// y === 7 true branch: atomic number narrows to the literal.
	got = mustQuery(t, res, "r2", Before, "y")
	if !got.Equal(NumberLiteral(7)) {
		t.Fatalf("true branch of y===7: got %s, want 7", got)
	}
	// y === 7 false branch: atomic number is NOT narrowed.
	got = mustQuery(t, res, "if2", After, "y")
	if !got.Equal(Number()) {
		t.Fatalf("false branch of y===7: got %s, want number", got)
	}
}

// Boolean is exactly true | false, so a strict equality against one
// literal splits it into the other on the false branch.
func TestBooleanSplit(t *testing.T) {
	decls := []Decl{decl("b", BooleanExpr())}
	body := []Stmt{
		&If{ID: "if", Cond: EqLiteral{Var: "b", Lit: BoolLit(true)},
			Then: []Stmt{&Assign{ID: "t", Var: "b", Type: BooleanExpr()}},
			Else: []Stmt{&Assign{ID: "f", Var: "b", Type: BooleanExpr()}}},
	}
	res := mustAnalyze(t, decls, body)
	if got := mustQuery(t, res, "t", Before, "b"); !got.Equal(BooleanLiteral(true)) {
		t.Fatalf("true branch: got %s, want true", got)
	}
	if got := mustQuery(t, res, "f", Before, "b"); !got.Equal(BooleanLiteral(false)) {
		t.Fatalf("false branch: got %s, want false", got)
	}
}

// Truthiness narrows atomic number to the zero literal and atomic
// string to the empty-string literal on the false branch, and removes
// falsy literal members on the true branch.
func TestTruthyNarrowing(t *testing.T) {
	decls := []Decl{
		decl("n", NumberExpr()),
		decl("s", StringExpr()),
		decl("m", UnionExpr(NumLitExpr(0), NumLitExpr(5), StrLitExpr(""), StrLitExpr("hi"), NullExpr())),
	}
	body := []Stmt{
		&If{ID: "ifN", Cond: Truthy{Var: "n"},
			Then: []Stmt{&Return{ID: "rN"}},
			Else: []Stmt{&Assign{ID: "fN", Var: "n", Type: NumberExpr()}}},
		&If{ID: "ifS", Cond: Truthy{Var: "s"},
			Then: []Stmt{&Return{ID: "rS"}},
			Else: []Stmt{&Assign{ID: "fS", Var: "s", Type: StringExpr()}}},
		&If{ID: "ifM", Cond: Truthy{Var: "m"},
			Then: []Stmt{&Return{ID: "rM"}},
			Else: []Stmt{&Assign{ID: "fM", Var: "m", Type: UnionExpr(NumLitExpr(0), NumLitExpr(5), StrLitExpr(""), StrLitExpr("hi"), NullExpr())}}},
	}
	res := mustAnalyze(t, decls, body)

	// False environments: n -> 0, s -> "".
	if got := mustQuery(t, res, "fN", Before, "n"); !got.Equal(NumberLiteral(0)) {
		t.Fatalf("falsy number: got %s, want 0", got)
	}
	if got := mustQuery(t, res, "fS", Before, "s"); !got.Equal(StringLiteral("")) {
		t.Fatalf("falsy string: got %s, want \"\"", got)
	}
	// True environment: falsy literal members removed, null removed.
	got := mustQuery(t, res, "rM", Before, "m")
	want := UnionOf(NumberLiteral(5), StringLiteral("hi"))
	if !got.Equal(want) {
		t.Fatalf("truthy literals: got %s, want %s", got, want)
	}
	// False environment of m: 0, "" and null survive; 5 and "hi" are gone.
	got = mustQuery(t, res, "fM", Before, "m")
	want = UnionOf(NumberLiteral(0), StringLiteral(""), Null())
	if !got.Equal(want) {
		t.Fatalf("falsy literals: got %s, want %s", got, want)
	}
}

// Loose equality with null hits both null and undefined.
func TestLooseEqNullDoubleHit(t *testing.T) {
	decls := []Decl{decl("x", UnionExpr(NullExpr(), UndefinedExpr(), NumberExpr()))}
	body := []Stmt{
		&If{ID: "if", Cond: LooseEqNull{Var: "x"},
			Then: []Stmt{&Return{ID: "r"}}},
	}
	res := mustAnalyze(t, decls, body)
	got := mustQuery(t, res, "r", Before, "x")
	if want := UnionOf(Null(), Undefined()); !got.Equal(want) {
		t.Fatalf("loose == null true branch: got %s, want %s", got, want)
	}
	if got := mustQuery(t, res, "if", After, "x"); !got.Equal(Number()) {
		t.Fatalf("loose == null false branch: got %s, want number", got)
	}
}

// Discriminant property: the true branch keeps only object members
// whose property type contains the literal; the false branch removes
// only members whose property type is exactly that literal.
func TestDiscriminantProperty(t *testing.T) {
	circle := ObjectExpr(Prop("kind", StrLitExpr("circle")), Prop("r", NumberExpr()))
	square := ObjectExpr(Prop("kind", StrLitExpr("square")), Prop("a", NumberExpr()))
	any := ObjectExpr(Prop("kind", UnionExpr(StrLitExpr("circle"), StrLitExpr("other"))))
	decls := []Decl{
		decl("s", UnionExpr(circle, square)),
		decl("m", UnionExpr(circle, any)),
	}
	body := []Stmt{
		&If{ID: "if", Cond: PropEq{Var: "s", Prop: "kind", Lit: StrLit("circle")},
			Then: []Stmt{&Assign{ID: "t", Var: "s", Type: UnionExpr(circle, square)}},
			Else: []Stmt{&Assign{ID: "f", Var: "s", Type: UnionExpr(circle, square)}}},
		&If{ID: "if2", Cond: PropEq{Var: "m", Prop: "kind", Lit: StrLit("circle")},
			Then: []Stmt{&Return{ID: "r2"}}},
	}
	res := mustAnalyze(t, decls, body)

	circleT := Object(map[string]Type{"kind": StringLiteral("circle"), "r": Number()})
	squareT := Object(map[string]Type{"kind": StringLiteral("square"), "a": Number()})
	anyT := Object(map[string]Type{"kind": UnionOf(StringLiteral("circle"), StringLiteral("other"))})

	// True branch keeps only the circle member.
	if got := mustQuery(t, res, "t", Before, "s"); !got.Equal(circleT) {
		t.Fatalf("discriminant true branch: got %s, want %s", got, circleT)
	}
	// False branch removes the member whose kind is exactly "circle".
	if got := mustQuery(t, res, "f", Before, "s"); !got.Equal(squareT) {
		t.Fatalf("discriminant false branch: got %s, want %s", got, squareT)
	}
	// A member whose property merely contains the literal survives the
	// false branch, because its property type is not exactly the literal;
	// the member whose kind is exactly "circle" is removed.
	got := mustQuery(t, res, "if2", After, "m")
	if want := anyT; !got.Equal(want) {
		t.Fatalf("discriminant false branch with union property: got %s, want %s", got, want)
	}
	if got := mustQuery(t, res, "r2", Before, "m"); !got.Equal(UnionOf(circleT, anyT)) {
		t.Fatalf("discriminant true branch with union property: got %s", got)
	}
}

// Short-circuit joins: the false environment of && and the true
// environment of || are per-variable unions of the two exit paths.
func TestShortCircuitJoins(t *testing.T) {
	decls := []Decl{
		decl("x", UnionExpr(NumLitExpr(1), NumLitExpr(2), NumLitExpr(3))),
		decl("y", UnionExpr(StrLitExpr("a"), StrLitExpr("b"))),
	}
	body := []Stmt{
		&If{ID: "and", Cond: And{EqLiteral{Var: "x", Lit: NumLit(1)}, EqLiteral{Var: "y", Lit: StrLit("a")}},
			Then: []Stmt{&Assign{ID: "andT", Var: "x", Type: UnionExpr(NumLitExpr(1), NumLitExpr(2), NumLitExpr(3))}},
			Else: []Stmt{&Assign{ID: "andF", Var: "x", Type: UnionExpr(NumLitExpr(1), NumLitExpr(2), NumLitExpr(3))}}},
		&If{ID: "or", Cond: Or{EqLiteral{Var: "x", Lit: NumLit(1)}, EqLiteral{Var: "y", Lit: StrLit("a")}},
			Then: []Stmt{&Assign{ID: "orT", Var: "x", Type: UnionExpr(NumLitExpr(1), NumLitExpr(2), NumLitExpr(3))}},
			Else: []Stmt{&Assign{ID: "orF", Var: "x", Type: UnionExpr(NumLitExpr(1), NumLitExpr(2), NumLitExpr(3))}}},
	}
	res := mustAnalyze(t, decls, body)

	// && true: both equalities hold.
	if got := mustQuery(t, res, "andT", Before, "x"); !got.Equal(NumberLiteral(1)) {
		t.Fatalf("&& true x: got %s, want 1", got)
	}
	if got := mustQuery(t, res, "andT", Before, "y"); !got.Equal(StringLiteral("a")) {
		t.Fatalf("&& true y: got %s, want \"a\"", got)
	}
	// && false: x may be 2|3 (left false) or 1 (left true, right false).
	if got := mustQuery(t, res, "andF", Before, "x"); !got.Equal(UnionOf(NumberLiteral(1), NumberLiteral(2), NumberLiteral(3))) {
		t.Fatalf("&& false x: got %s, want 1|2|3", got)
	}
	if got := mustQuery(t, res, "andF", Before, "y"); !got.Equal(UnionOf(StringLiteral("a"), StringLiteral("b"))) {
		t.Fatalf("&& false y: got %s, want \"a\"|\"b\"", got)
	}
	// || true: x may be 1 (left true) or 2|3 (left false, right true).
	if got := mustQuery(t, res, "orT", Before, "x"); !got.Equal(UnionOf(NumberLiteral(1), NumberLiteral(2), NumberLiteral(3))) {
		t.Fatalf("|| true x: got %s, want 1|2|3", got)
	}
	if got := mustQuery(t, res, "orT", Before, "y"); !got.Equal(UnionOf(StringLiteral("a"), StringLiteral("b"))) {
		t.Fatalf("|| true y: got %s, want \"a\"|\"b\"", got)
	}
	// || false: both equalities fail.
	if got := mustQuery(t, res, "orF", Before, "x"); !got.Equal(UnionOf(NumberLiteral(2), NumberLiteral(3))) {
		t.Fatalf("|| false x: got %s, want 2|3", got)
	}
	if got := mustQuery(t, res, "orF", Before, "y"); !got.Equal(StringLiteral("b")) {
		t.Fatalf("|| false y: got %s, want \"b\"", got)
	}
}

// An early return removes the matching path from the join after the
// conditional.
func TestEarlyReturnJoin(t *testing.T) {
	decls := []Decl{decl("x", UnionExpr(NumberExpr(), StringExpr()))}
	body := []Stmt{
		&If{ID: "if", Cond: TypeOf{Var: "x", Kind: TypeOfString},
			Then: []Stmt{&Return{ID: "r"}}},
		&Assign{ID: "after", Var: "x", Type: NumberExpr()},
	}
	res := mustAnalyze(t, decls, body)
	if got := mustQuery(t, res, "after", Before, "x"); !got.Equal(Number()) {
		t.Fatalf("after early return: got %s, want number", got)
	}
}

// A point unreachable on every path is reported as unreachable, which
// is distinguishable from a present narrowing result.
func TestUnreachable(t *testing.T) {
	decls := []Decl{decl("x", NumLitExpr(1))}
	body := []Stmt{
		&If{ID: "if", Cond: EqLiteral{Var: "x", Lit: NumLit(2)},
			Then: []Stmt{&Assign{ID: "dead", Var: "x", Type: NumLitExpr(1)}}},
		&Return{ID: "ret"},
		&Assign{ID: "afterRet", Var: "x", Type: NumLitExpr(1)},
	}
	res := mustAnalyze(t, decls, body)

	for _, id := range []StatementID{"dead", "afterRet"} {
		_, reachable, ok := res.Query(id, Before, "x")
		if !ok {
			t.Fatalf("%s: query must succeed", id)
		}
		if reachable {
			t.Fatalf("%s: must be unreachable", id)
		}
	}
	// The if statement itself joins only the (reachable) false branch.
	if got := mustQuery(t, res, "if", After, "x"); !got.Equal(NumberLiteral(1)) {
		t.Fatalf("if join: got %s, want 1", got)
	}
	// Unknown statement / variable are distinguishable from unreachable.
	if _, _, ok := res.Query("nope", Before, "x"); ok {
		t.Fatal("unknown statement id must not be ok")
	}
	if _, _, ok := res.Query("if", Before, "nope"); ok {
		t.Fatal("unknown variable must not be ok")
	}
}

// Assignment resets the narrowed type to exactly the assigned type.
func TestAssignmentResetsNarrowing(t *testing.T) {
	decls := []Decl{
		decl("x", UnionExpr(NumberExpr(), StringExpr())),
		decl("y", NumberExpr()),
	}
	body := []Stmt{
		&If{ID: "if", Cond: TypeOf{Var: "x", Kind: TypeOfString},
			Then: []Stmt{&Assign{ID: "reset", Var: "x", Type: UnionExpr(NumberExpr(), StringExpr())}}},
		&If{ID: "if2", Cond: EqLiteral{Var: "y", Lit: NumLit(1)},
			Then: []Stmt{&Assign{ID: "widen", Var: "y", Type: NumberExpr()}}},
	}
	res := mustAnalyze(t, decls, body)
	if got := mustQuery(t, res, "reset", After, "x"); !got.Equal(UnionOf(Number(), StringType())) {
		t.Fatalf("assignment must reset narrowing: got %s", got)
	}
	if got := mustQuery(t, res, "widen", After, "y"); !got.Equal(Number()) {
		t.Fatalf("assignment must widen back to assigned type: got %s", got)
	}
}
