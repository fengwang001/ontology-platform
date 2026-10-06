package narrowing

import (
	"math"
	"testing"
)

func TestUnionNormalization(t *testing.T) {
	// Flattening, deduplication and literal absorption.
	got := UnionOf(UnionOf(Number(), NumberLiteral(3)), Number())
	if !got.Equal(Number()) {
		t.Fatalf("number absorbs 3: got %s", got)
	}
	got = UnionOf(StringType(), StringLiteral("a"), StringLiteral("b"))
	if !got.Equal(StringType()) {
		t.Fatalf("string absorbs literals: got %s", got)
	}
	// Literals of a different class survive.
	got = UnionOf(Number(), StringLiteral("a"))
	want := UnionOf(StringLiteral("a"), Number())
	if !got.Equal(want) {
		t.Fatalf("union member order must not matter: %s vs %s", got, want)
	}
	if got.Equal(Number()) {
		t.Fatal("number | \"a\" must differ from number")
	}
}

func TestNeverAndSingleton(t *testing.T) {
	if !Never().IsNever() {
		t.Fatal("empty union is never")
	}
	if !UnionOf().IsNever() {
		t.Fatal("union of nothing is never")
	}
	single := UnionOf(NumberLiteral(1))
	if !single.Equal(NumberLiteral(1)) {
		t.Fatalf("single-member union equals its member: %s", single)
	}
	if UnionOf(Never(), Number()).Equal(Never()) {
		t.Fatal("never is identity for union, not absorbing")
	}
}

func TestBooleanIsLiteralUnion(t *testing.T) {
	if !Boolean().Equal(UnionOf(BooleanLiteral(true), BooleanLiteral(false))) {
		t.Fatal("boolean must equal true | false")
	}
	if Boolean().Equal(BooleanLiteral(true)) {
		t.Fatal("boolean must differ from true")
	}
}

func TestObjectEquality(t *testing.T) {
	a := Object(map[string]Type{"x": Number(), "y": StringType()})
	b := Object(map[string]Type{"y": StringType(), "x": Number()})
	if !a.Equal(b) {
		t.Fatal("object equality must be order-independent")
	}
	c := Object(map[string]Type{"x": Number()})
	if a.Equal(c) {
		t.Fatal("objects with different property sets must differ")
	}
	d := Object(map[string]Type{"x": Number(), "y": Number()})
	if a.Equal(d) {
		t.Fatal("objects with different property types must differ")
	}
}

func TestMembership(t *testing.T) {
	if !Number().ContainsType(NumberLiteral(42)) {
		t.Fatal("number must contain every number literal")
	}
	if NumberLiteral(42).ContainsType(Number()) {
		t.Fatal("a literal must not contain the atomic type")
	}
	if !Boolean().ContainsType(BooleanLiteral(false)) {
		t.Fatal("boolean must contain false")
	}
	if UnionOf(Number(), Null()).ContainsType(StringLiteral("a")) {
		t.Fatal("string literal must not belong to number | null")
	}
	obj := Object(map[string]Type{"k": Number()})
	if !obj.ContainsType(obj) {
		t.Fatal("object type must contain itself")
	}
}

func TestNegativeZeroAndNaN(t *testing.T) {
	if !NumberLiteral(0).Equal(NumberLiteral(math.Copysign(0, -1))) {
		t.Fatal("-0 must normalize to 0")
	}
	if _, err := NumLitExpr(math.NaN()).normalize(); err == nil {
		t.Fatal("NaN literal must be malformed")
	}
}
