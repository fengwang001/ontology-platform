package consteval

import (
	"errors"
	"math"
	"math/big"
	"testing"
)

func errKind(t *testing.T, err error) ErrKind {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not a *consteval.Error", err)
	}
	return e.Kind
}

func mustEval(t *testing.T, e *Expr) Value {
	t.Helper()
	v, err := Eval(e, nil)
	if err != nil {
		t.Fatalf("Eval failed: %v", err)
	}
	return v
}

func expectErr(t *testing.T, e *Expr, want ErrKind) {
	t.Helper()
	_, err := Eval(e, nil)
	if got := errKind(t, err); got != want {
		t.Fatalf("error kind = %v, want %v (err=%v)", got, want, err)
	}
}

func pow2(n uint) string {
	return new(big.Int).Lsh(big.NewInt(1), n).String()
}

func pow2minus(n, k uint) string {
	i := new(big.Int).Lsh(big.NewInt(1), n)
	i.Sub(i, new(big.Int).Lsh(big.NewInt(1), k))
	return i.String()
}

// Untyped arbitrary-precision intermediates may leave and re-enter a type's
// range; typed arithmetic must be representable at every single step.
func TestUntypedIntermediateVsTypedPerStepOverflow(t *testing.T) {
	big70 := Shl(IntLit("1"), IntLit("70")) // 2^70, far beyond int64
	// Untyped: (2^70) - (2^70 - 5) == 5, representable in int64 at the end.
	v := mustEval(t, To(Int64, Sub(big70, Sub(big70, IntLit("5")))))
	if v.Int().Int64() != 5 || v.Type() != Int64 {
		t.Fatalf("got %v, want int64 5", v)
	}
	// Typed: converting 2^70 to int64 overflows immediately.
	expectErr(t, To(Int64, big70), ErrOverflow)
	// Typed per-step: int64 max + 1 overflows even though a later
	// subtraction would bring the value back into range.
	reg := NewRegistry()
	if _, err := reg.Register("max", "int64", IntLit("9223372036854775807")); err != nil {
		t.Fatal(err)
	}
	step := Sub(Add(NameRef("max"), IntLit("1")), IntLit("1"))
	if _, err := Eval(step, reg); errKind(t, err) != ErrOverflow {
		t.Fatalf("typed per-step overflow: got %v", err)
	}
	// The same shape untyped is fine.
	if v := mustEval(t, Sub(Add(IntLit("9223372036854775807"), IntLit("1")), IntLit("1"))); v.Int().String() != "9223372036854775807" {
		t.Fatalf("untyped round trip = %v", v)
	}
}

// int/int divides with truncation; any rational operand forces exact
// division; the rational kind sticks even when the value is integral.
func TestDivisionKindSelection(t *testing.T) {
	if v := mustEval(t, Quo(IntLit("7"), IntLit("2"))); v.KindOf() != IntKind || v.Int().Int64() != 3 {
		t.Fatalf("7/2 = %v, want untyped int 3", v)
	}
	if v := mustEval(t, Quo(IntLit("-7"), IntLit("2"))); v.Int().Int64() != -3 {
		t.Fatalf("-7/2 = %v, want -3 (truncated)", v)
	}
	for _, e := range []*Expr{
		Quo(RatLit("7"), IntLit("2")),
		Quo(IntLit("7"), RatLit("2")),
		Quo(RatLit("7"), RatLit("2")),
	} {
		v := mustEval(t, e)
		if v.KindOf() != RatKind || v.Rat().RatString() != "7/2" {
			t.Fatalf("exact division = %v (%s), want rat 7/2", v, v.KindOf())
		}
	}
	// 4/2 over rationals is exactly 2 but keeps the rational kind...
	v := mustEval(t, Quo(RatLit("4"), RatLit("2")))
	if v.KindOf() != RatKind || v.Rat().RatString() != "2" {
		t.Fatalf("4/2 over rats = %v (%s)", v, v.KindOf())
	}
	// ...so it cannot feed a remainder operation.
	expectErr(t, Rem(Quo(RatLit("4"), RatLit("2")), IntLit("1")), ErrIllegalOp)
}

func TestNegativeRemAndRightShift(t *testing.T) {
	cases := []struct {
		e    *Expr
		want int64
	}{
		{Rem(IntLit("-7"), IntLit("3")), -1},
		{Rem(IntLit("7"), IntLit("-3")), 1},
		{Rem(IntLit("-7"), IntLit("-3")), -1},
		{Shr(IntLit("-7"), IntLit("1")), -4}, // floor(-3.5)
		{Shr(IntLit("-1"), IntLit("1")), -1},
		{Shr(IntLit("-8"), IntLit("2")), -2},
	}
	for _, c := range cases {
		if v := mustEval(t, c.e); v.Int().Int64() != c.want {
			t.Fatalf("got %d, want %d", v.Int().Int64(), c.want)
		}
	}
}

func TestFloatRoundingTiesToEvenAndInfinity(t *testing.T) {
	floatOf := func(e *Expr) float64 {
		t.Helper()
		v := mustEval(t, To(Float64, e))
		if v.Type() != Float64 {
			t.Fatalf("type = %v, want float64", v.Type())
		}
		return v.Float()
	}
	// 1 + 2^-53 is the exact midpoint of 1.0 and 1+2^-52; ties to even -> 1.0.
	num := new(big.Int).Lsh(big.NewInt(1), 53)
	den := new(big.Int).Set(num)
	num.Add(num, big.NewInt(1))
	if got := floatOf(RatLit(num.String() + "/" + den.String())); got != 1.0 {
		t.Fatalf("1+2^-53 rounded to %v, want 1.0 (ties to even)", got)
	}
	// 1 + 3*2^-53 is the midpoint of 1+2^-52 (odd mantissa) and 1+2^-51
	// (even mantissa); ties to even -> 1+2^-51.
	num.Add(num, big.NewInt(2))
	if want := 1 + math.Ldexp(1, -51); floatOf(RatLit(num.String()+"/"+den.String())) != want {
		t.Fatalf("1+3*2^-53 did not round to %v (ties to even)", want)
	}
	// Integer kind to float rounds the same way.
	if got := floatOf(IntLit("9007199254740993")); got != 9007199254740992.0 {
		t.Fatalf("2^53+1 -> %v, want 2^53 (ties to even)", got)
	}
	if got := floatOf(IntLit("9007199254740995")); got != 9007199254740996.0 {
		t.Fatalf("2^53+3 -> %v, want 2^53+4 (ties to even)", got)
	}
	// Rounding to infinity is overflow.
	expectErr(t, To(Float64, RatLit(pow2(1024))), ErrOverflow)
	// The largest finite double, 2^1024 - 2^971, is representable.
	if got := floatOf(RatLit(pow2minus(1024, 971))); got != math.MaxFloat64 {
		t.Fatalf("2^1024-2^971 -> %v, want MaxFloat64", got)
	}
	// The midpoint 2^1024 - 2^970 ties to the even mantissa, i.e. 2^1024,
	// which is infinite in float64: overflow.
	expectErr(t, To(Float64, RatLit(pow2minus(1024, 970))), ErrOverflow)
	// One below that midpoint still rounds down to MaxFloat64.
	below, _ := new(big.Int).SetString(pow2minus(1024, 970), 10)
	below.Sub(below, big.NewInt(1))
	if got := floatOf(RatLit(below.String())); got != math.MaxFloat64 {
		t.Fatalf("2^1024-2^970-1 -> %v, want MaxFloat64", got)
	}
}

// Conversion to integer types distinguishes truncation (not integral) from
// overflow (out of range), for both the integer and the rational kind.
func TestConversionBoundaries(t *testing.T) {
	ok := []struct {
		e    *Expr
		want int64
	}{
		{To(Int64, IntLit("9223372036854775807")), math.MaxInt64},
		{To(Int64, IntLit("-9223372036854775808")), math.MinInt64},
		{To(Int64, RatLit("4/2")), 2}, // integral rational converts
		{To(Int8, IntLit("127")), 127},
		{To(Int8, IntLit("-128")), -128},
		{To(Uint8, IntLit("255")), 255},
		{To(Uint8, IntLit("0")), 0},
	}
	for _, c := range ok {
		if v := mustEval(t, c.e); v.Int().Int64() != c.want {
			t.Fatalf("got %v, want %d", v, c.want)
		}
	}
	// Overflow: integral but out of range (both kinds).
	for _, e := range []*Expr{
		To(Int64, IntLit(pow2(63))),
		To(Int64, RatLit(pow2(63))), // integral rational, same boundary
		To(Int8, IntLit("128")),
		To(Int8, IntLit("-129")),
		To(Uint8, IntLit("-1")),
		To(Uint8, IntLit("256")),
	} {
		expectErr(t, e, ErrOverflow)
	}
	// Truncation: not integral, distinct from overflow.
	for _, e := range []*Expr{
		To(Int64, RatLit("3/2")),
		To(Int8, RatLit("1/2")),
		To(Uint8, RatLit("-1/2")),
	} {
		expectErr(t, e, ErrTruncation)
	}
	// Kind mismatches are type mismatches, not representability failures.
	expectErr(t, To(Int64, BoolLit(true)), ErrTypeMismatch)
	expectErr(t, To(Float64, StrLit("x")), ErrTypeMismatch)
	expectErr(t, To(Bool, IntLit("1")), ErrTypeMismatch)
	expectErr(t, To(String, IntLit("1")), ErrTypeMismatch)
}

// Untyped integers may use at most 512 bits of magnitude; any intermediate
// result beyond that is "constant too large", even if a later step would
// shrink it again.
func TestUntypedInt512BitBoundary(t *testing.T) {
	if v := mustEval(t, IntLit(pow2minus(512, 0))); v.Int().BitLen() != 512 {
		t.Fatalf("2^512-1 should be representable, got bitlen %d", v.Int().BitLen())
	}
	expectErr(t, IntLit(pow2(512)), ErrConstTooLarge)
	expectErr(t, Shl(IntLit("1"), IntLit("512")), ErrConstTooLarge)
	if v := mustEval(t, Shl(IntLit("1"), IntLit("511"))); v.Int().BitLen() != 512 {
		t.Fatalf("1<<511 bitlen = %d", v.Int().BitLen())
	}
	// Multiplication crossing the boundary mid-tree.
	expectErr(t, Mul(IntLit(pow2(256)), IntLit(pow2(256))), ErrConstTooLarge)
	// Negation keeps the magnitude: -(2^512-1) is fine, -2^512 is not.
	if v := mustEval(t, Neg(IntLit(pow2minus(512, 0)))); v.Int().BitLen() != 512 {
		t.Fatalf("-(2^512-1) bitlen = %d", v.Int().BitLen())
	}
	expectErr(t, Shl(IntLit("-1"), IntLit("512")), ErrConstTooLarge)
	// An intermediate overflow is reported even though the final value
	// would be tiny: (2^600) - (2^600 - 1) fails at the first shift.
	huge := Shl(IntLit("1"), IntLit("600"))
	expectErr(t, Sub(huge, Sub(huge, IntLit("1"))), ErrConstTooLarge)
}

// All subexpressions are evaluated: errors inside branches that a logical
// operator would short-circuit are still reported.
func TestNoShortCircuit(t *testing.T) {
	expectErr(t,
		LAnd(BoolLit(false), Eql(Quo(IntLit("1"), IntLit("0")), IntLit("0"))),
		ErrDivByZero)
	expectErr(t,
		LOr(BoolLit(true), Rem(IntLit("1"), RatLit("1"))),
		ErrIllegalOp)
	// Well-formed logical operations still compute plain boolean results.
	if v := mustEval(t, LAnd(BoolLit(true), BoolLit(false))); v.Bool() != false {
		t.Fatalf("true && false = %v", v)
	}
	if v := mustEval(t, LOr(BoolLit(false), BoolLit(true))); v.Bool() != true {
		t.Fatalf("false || true = %v", v)
	}
}

// Within one node: type mismatch > illegal operation > division by zero >
// representability. Across the tree: children before parents, left first.
func TestErrorPriority(t *testing.T) {
	// Kind mismatch beats "operator not defined for this kind".
	expectErr(t, Add(IntLit("1"), BoolLit(true)), ErrTypeMismatch)
	expectErr(t, Add(BoolLit(true), IntLit("1")), ErrTypeMismatch)
	// Same kind, unsupported operator: illegal operation.
	expectErr(t, Sub(StrLit("a"), StrLit("b")), ErrIllegalOp)
	expectErr(t, LAnd(IntLit("1"), IntLit("1")), ErrIllegalOp)
	expectErr(t, Not(IntLit("1")), ErrIllegalOp)
	expectErr(t, BitNot(RatLit("2")), ErrIllegalOp)
	// Illegal operation beats division by zero: % on a rational zero.
	expectErr(t, Rem(IntLit("1"), RatLit("0")), ErrIllegalOp)
	// Division by zero beats representability: the untyped dividend does
	// not fit int8, but the zero divisor is reported first.
	expectErr(t, Quo(IntLit(pow2(60)), To(Int8, IntLit("0"))), ErrDivByZero)
	// Plain division by zero.
	expectErr(t, Quo(IntLit("1"), IntLit("0")), ErrDivByZero)
	expectErr(t, Quo(RatLit("1"), RatLit("0")), ErrDivByZero)
	expectErr(t, Rem(IntLit("1"), IntLit("0")), ErrDivByZero)
	// Children before parents, left to right: the left child's division by
	// zero beats the right child's illegal operation, and vice versa.
	expectErr(t,
		Add(Quo(IntLit("1"), IntLit("0")), Rem(IntLit("1"), RatLit("1"))),
		ErrDivByZero)
	expectErr(t,
		Add(Rem(IntLit("1"), RatLit("1")), Quo(IntLit("1"), IntLit("0"))),
		ErrIllegalOp)
	// Structural problems beat every evaluation error.
	badArity := &Expr{Node: Binary, Op: OpAdd, Args: []*Expr{Quo(IntLit("1"), IntLit("0"))}}
	expectErr(t, badArity, ErrInvalidArgument)
	expectErr(t, NameRef(""), ErrInvalidArgument)
	expectErr(t, &Expr{Node: Unary, Op: OpAdd, Args: []*Expr{IntLit("1")}}, ErrInvalidArgument)
	expectErr(t, IntLit("not-a-number"), ErrInvalidArgument)
	expectErr(t, RatLit("1/0"), ErrInvalidArgument)
	// Unknown names beat evaluation errors.
	reg := NewRegistry()
	if _, err := Eval(Quo(NameRef("ghost"), IntLit("0")), reg); errKind(t, err) != ErrUnknownName {
		t.Fatalf("unknown name vs div by zero: got %v", err)
	}
}

// Negation and bitwise complement of typed unsigned integers.
func TestUnsignedSemantics(t *testing.T) {
	reg := NewRegistry()
	mustRegister := func(name, typ string, e *Expr) {
		t.Helper()
		if _, err := reg.Register(name, typ, e); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	mustRegister("u", "uint8", IntLit("5"))
	mustRegister("z", "uint8", IntLit("0"))
	mustRegister("s", "int8", IntLit("127"))
	mustRegister("sm", "int8", IntLit("-128"))
	// Negating a non-zero unsigned value is out of range; zero is fine.
	if _, err := Eval(Neg(NameRef("u")), reg); errKind(t, err) != ErrOverflow {
		t.Fatalf("negate uint8 5: got %v", err)
	}
	if v, err := Eval(Neg(NameRef("z")), reg); err != nil || v.Int().Int64() != 0 {
		t.Fatalf("negate uint8 0 = %v, %v", v, err)
	}
	// Bitwise complement: unsigned flips within the width, signed and
	// untyped compute -x-1.
	if v, _ := Eval(BitNot(NameRef("u")), reg); v.Int().Int64() != 250 || v.Type() != Uint8 {
		t.Fatalf("^uint8(5) = %v, want uint8 250", v)
	}
	if v, _ := Eval(BitNot(NameRef("s")), reg); v.Int().Int64() != -128 {
		t.Fatalf("^int8(127) = %v, want -128", v)
	}
	if v, _ := Eval(BitNot(NameRef("sm")), reg); v.Int().Int64() != 127 {
		t.Fatalf("^int8(-128) = %v, want 127", v)
	}
	if v := mustEval(t, BitNot(IntLit("5"))); v.Int().Int64() != -6 {
		t.Fatalf("^5 = %v, want -6", v)
	}
}

// Mixing untyped and typed operands converts the untyped side; two typed
// operands must agree exactly; every typed step is range-checked.
func TestMixedTypedUntyped(t *testing.T) {
	reg := NewRegistry()
	if _, err := reg.Register("a", "int8", IntLit("100")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register("b", "int16", IntLit("1")); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Register("f", "float64", RatLit("1/2")); err != nil {
		t.Fatal(err)
	}
	// Untyped converts to the typed side's type.
	v, err := Eval(Add(NameRef("a"), IntLit("10")), reg)
	if err != nil || v.Type() != Int8 || v.Int().Int64() != 110 {
		t.Fatalf("int8(100)+10 = %v, %v; want int8 110", v, err)
	}
	// Result overflows the type at this step.
	if _, err := Eval(Add(NameRef("a"), IntLit("100")), reg); errKind(t, err) != ErrOverflow {
		t.Fatalf("int8(100)+100: got %v", err)
	}
	// The untyped operand itself is not representable.
	if _, err := Eval(Add(NameRef("a"), IntLit("200")), reg); errKind(t, err) != ErrOverflow {
		t.Fatalf("int8(100)+200: got %v", err)
	}
	// Two different concrete types never mix.
	if _, err := Eval(Add(NameRef("a"), NameRef("b")), reg); errKind(t, err) != ErrTypeMismatch {
		t.Fatalf("int8 + int16: got %v", err)
	}
	// Untyped rational converts to float64 with rounding.
	v, err = Eval(Add(NameRef("f"), RatLit("1/4")), reg)
	if err != nil || v.Type() != Float64 || v.Float() != 0.75 {
		t.Fatalf("0.5 + 1/4 = %v, %v; want float64 0.75", v, err)
	}
	// Untyped rational into a sized integer: truncation vs overflow.
	if _, err := Eval(Add(NameRef("a"), RatLit("3/2")), reg); errKind(t, err) != ErrTruncation {
		t.Fatalf("int8 + 3/2: got %v", err)
	}
}

func TestStrings(t *testing.T) {
	if v := mustEval(t, Add(StrLit("foo"), StrLit("bar"))); v.Str() != "foobar" {
		t.Fatalf("concat = %v", v)
	}
	// Byte-order comparison: 'Z' (0x5A) < 'a' (0x61).
	if v := mustEval(t, Lss(StrLit("Z"), StrLit("a"))); !v.Bool() {
		t.Fatalf(`"Z" < "a" should hold in byte order`)
	}
	if v := mustEval(t, Geq(StrLit("abc"), StrLit("abd"))); v.Bool() {
		t.Fatalf(`"abc" >= "abd" should not hold`)
	}
	if v := mustEval(t, Eql(StrLit("x"), StrLit("x"))); !v.Bool() {
		t.Fatalf(`"x" == "x" should hold`)
	}
	// Typed strings concatenate and stay typed.
	reg := NewRegistry()
	if _, err := reg.Register("s", "string", StrLit("x")); err != nil {
		t.Fatal(err)
	}
	v, err := Eval(Add(NameRef("s"), StrLit("y")), reg)
	if err != nil || v.Type() != String || v.Str() != "xy" {
		t.Fatalf("typed concat = %v, %v", v, err)
	}
	// Strings support nothing beyond + and comparisons.
	expectErr(t, Mul(StrLit("a"), StrLit("b")), ErrIllegalOp)
	expectErr(t, Neg(StrLit("a")), ErrIllegalOp)
}

func TestShifts(t *testing.T) {
	if v := mustEval(t, Shl(IntLit("3"), IntLit("4"))); v.Int().Int64() != 48 {
		t.Fatalf("3<<4 = %v", v)
	}
	// Illegal counts: negative, above 1000, or not of integer kind.
	expectErr(t, Shl(IntLit("1"), IntLit("-1")), ErrIllegalOp)
	expectErr(t, Shl(IntLit("1"), IntLit("1001")), ErrIllegalOp)
	expectErr(t, Shl(IntLit("1"), RatLit("2")), ErrIllegalOp)
	// A rational left operand is illegal even when its value is integral.
	expectErr(t, Shl(RatLit("4"), IntLit("1")), ErrIllegalOp)
	expectErr(t, Shr(RatLit("4/2"), IntLit("1")), ErrIllegalOp)
	// Typed left shifts must be representable; right shifts round down.
	expectErr(t, Shl(To(Int8, IntLit("1")), IntLit("7")), ErrOverflow)
	if v := mustEval(t, Shl(To(Uint8, IntLit("1")), IntLit("7"))); v.Int().Int64() != 128 {
		t.Fatalf("uint8 1<<7 = %v", v)
	}
	if v := mustEval(t, Shr(To(Int8, IntLit("-7")), IntLit("1"))); v.Int().Int64() != -4 {
		t.Fatalf("int8 -7>>1 = %v, want -4", v)
	}
	// The count's type does not leak into the result.
	reg := NewRegistry()
	if _, err := reg.Register("n", "uint16", IntLit("3")); err != nil {
		t.Fatal(err)
	}
	v, err := Eval(Shl(IntLit("1"), NameRef("n")), reg)
	if err != nil || v.Type() != NoType || v.Int().Int64() != 8 {
		t.Fatalf("1 << uint16(3) = %v (%v), %v; want untyped 8", v, v.Type(), err)
	}
}

// Contexts that need a concrete type use the defaults: int64, float64,
// bool, string.
func TestDefaultTypes(t *testing.T) {
	cases := []struct {
		e    *Expr
		want Type
	}{
		{IntLit("5"), Int64},
		{RatLit("1/2"), Float64},
		{BoolLit(true), Bool},
		{StrLit("s"), String},
		{To(Int8, IntLit("5")), Int8},
	}
	for _, c := range cases {
		if got := mustEval(t, c.e).EffectiveType(); got != c.want {
			t.Fatalf("effective type = %v, want %v", got, c.want)
		}
	}
}
