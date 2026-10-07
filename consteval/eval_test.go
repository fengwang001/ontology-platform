package consteval

import (
	"math/big"
	"testing"
)

func errKindOf(err error) ErrKind {
	if err == nil {
		return -1
	}
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return -2
}

// mustEval evaluates expr in a fresh registry and fails on error.
func mustEval(t *testing.T, expr *Node) *Const {
	t.Helper()
	v, err := NewRegistry().Eval(expr)
	if err != nil {
		t.Fatalf("Eval(%s) unexpected error: %v", expr, err)
	}
	return v
}

// mustErr evaluates expr in a fresh registry and requires an error of
// the given kind.
func mustErr(t *testing.T, expr *Node, kind ErrKind) {
	t.Helper()
	v, err := NewRegistry().Eval(expr)
	if err == nil {
		t.Fatalf("Eval(%s) = %s, want error %s", expr, v, kind)
	}
	if got := errKindOf(err); got != kind {
		t.Fatalf("Eval(%s) error = %v (kind %s), want kind %s", expr, err, got, kind)
	}
}

func pow2(n uint) *big.Int { return new(big.Int).Lsh(big.NewInt(1), n) }

func wantUntypedInt(t *testing.T, v *Const, want string) {
	t.Helper()
	if v.Type() != TypeNone || v.Kind() != KindInt {
		t.Fatalf("got %s, want untyped int %s", v, want)
	}
	if got := v.Int().String(); got != want {
		t.Fatalf("got untyped int %s, want %s", got, want)
	}
}

func wantUntypedRat(t *testing.T, v *Const, want string) {
	t.Helper()
	if v.Type() != TypeNone || v.Kind() != KindRational {
		t.Fatalf("got %s, want untyped rational %s", v, want)
	}
	if got := v.Rat().RatString(); got != want {
		t.Fatalf("got untyped rational %s, want %s", got, want)
	}
}

func wantUntypedBool(t *testing.T, v *Const, want bool) {
	t.Helper()
	if v.Type() != TypeNone || v.Kind() != KindBool || v.Bool() != want {
		t.Fatalf("got %s, want untyped bool %t", v, want)
	}
}

func wantTypedInt(t *testing.T, v *Const, typ Type, want int64) {
	t.Helper()
	if v.Type() != typ || v.Int64() != want {
		t.Fatalf("got %s, want %s %d", v, typ, want)
	}
}

func wantTypedUint(t *testing.T, v *Const, typ Type, want uint64) {
	t.Helper()
	if v.Type() != typ || v.Uint64() != want {
		t.Fatalf("got %s, want %s %d", v, typ, want)
	}
}

// An untyped intermediate may exceed int64 as long as the final value
// comes back into range; a typed computation overflows at the exact
// step that leaves the type's range, with no wraparound.
func TestUntypedLargeIntermediateVsTypedPerStepOverflow(t *testing.T) {
	// (2^62 * 8) / 16 = 2^61; intermediate 2^65 overflows int64.
	expr := Bin(OpDiv, Bin(OpMul, BigIntLit(pow2(62)), IntLit("8")), IntLit("16"))
	wantUntypedInt(t, mustEval(t, expr), pow2(61).String())

	typed := Bin(OpDiv,
		Bin(OpMul, Convert(BigIntLit(pow2(62)), "int64"), Convert(IntLit("8"), "int64")),
		Convert(IntLit("16"), "int64"))
	mustErr(t, typed, ErrOverflow)

	// The same untyped expression is registrable without a type: the
	// final value 2^61 fits the default int64.
	r := NewRegistry()
	if _, err := r.Register("c", expr, ""); err != nil {
		t.Fatalf("Register untyped large-intermediate expr: %v", err)
	}
}

// Integer-kind division truncates toward zero; as soon as one operand
// is of rational kind the division is exact. A rational whose value is
// integral keeps the rational kind.
func TestDivisionKindSelection(t *testing.T) {
	wantUntypedInt(t, mustEval(t, Bin(OpDiv, IntLit("7"), IntLit("2"))), "3")
	wantUntypedInt(t, mustEval(t, Bin(OpDiv, IntLit("-7"), IntLit("2"))), "-3")
	wantUntypedInt(t, mustEval(t, Bin(OpDiv, IntLit("7"), IntLit("-2"))), "-3")

	wantUntypedRat(t, mustEval(t, Bin(OpDiv, RatLit("7/1"), IntLit("2"))), "7/2")
	wantUntypedRat(t, mustEval(t, Bin(OpDiv, IntLit("7"), RatLit("2/1"))), "7/2")
	wantUntypedRat(t, mustEval(t, Bin(OpDiv, RatLit("7/2"), RatLit("1/2"))), "7")

	// Rational kind is preserved even when the value is integral.
	v := mustEval(t, Bin(OpDiv, RatLit("4/1"), RatLit("2/1")))
	wantUntypedRat(t, v, "2")

	// Typed integer division truncates; typed float division is exact.
	wantTypedInt(t, mustEval(t, Bin(OpDiv,
		Convert(IntLit("-7"), "int8"), Convert(IntLit("2"), "int8"))), TypeInt8, -3)
	f := mustEval(t, Bin(OpDiv, Convert(IntLit("7"), "float64"), Convert(IntLit("2"), "float64")))
	if f.Type() != TypeFloat64 || f.Float64() != 3.5 {
		t.Fatalf("got %s, want float64 3.5", f)
	}
}

// Truncated remainder takes the dividend's sign; right shift rounds
// toward negative infinity (arithmetic shift).
func TestNegativeModAndRightShift(t *testing.T) {
	wantUntypedInt(t, mustEval(t, Bin(OpMod, IntLit("-7"), IntLit("3"))), "-1")
	wantUntypedInt(t, mustEval(t, Bin(OpMod, IntLit("7"), IntLit("-3"))), "1")
	wantUntypedInt(t, mustEval(t, Bin(OpMod, IntLit("-7"), IntLit("-3"))), "-1")

	wantUntypedInt(t, mustEval(t, Bin(OpShr, IntLit("-7"), IntLit("1"))), "-4")
	wantUntypedInt(t, mustEval(t, Bin(OpShr, IntLit("-8"), IntLit("1"))), "-4")
	wantUntypedInt(t, mustEval(t, Bin(OpShr, IntLit("-1"), IntLit("100"))), "-1")

	wantTypedInt(t, mustEval(t, Bin(OpShr,
		Convert(IntLit("-7"), "int8"), Convert(IntLit("1"), "int8"))), TypeInt8, -4)
	wantTypedInt(t, mustEval(t, Bin(OpMod,
		Convert(IntLit("-7"), "int16"), Convert(IntLit("3"), "int16"))), TypeInt16, -1)
}

// Bitwise negation: untyped and typed signed ^x == -x-1; typed unsigned
// flips the bits within the type's width. Negating a nonzero unsigned
// constant is out of range.
func TestBitNotAndUnsignedNeg(t *testing.T) {
	wantUntypedInt(t, mustEval(t, Un(OpBitNot, IntLit("5"))), "-6")
	wantUntypedInt(t, mustEval(t, Un(OpBitNot, IntLit("-1"))), "0")

	wantTypedInt(t, mustEval(t, Un(OpBitNot, Convert(IntLit("5"), "int8"))), TypeInt8, -6)
	wantTypedInt(t, mustEval(t, Un(OpBitNot, Convert(IntLit("-128"), "int8"))), TypeInt8, 127)
	wantTypedUint(t, mustEval(t, Un(OpBitNot, Convert(IntLit("5"), "uint8"))), TypeUint8, 250)
	wantTypedUint(t, mustEval(t, Un(OpBitNot, Convert(IntLit("0"), "uint16"))), TypeUint16, 65535)

	wantTypedUint(t, mustEval(t, Un(OpNeg, Convert(IntLit("0"), "uint8"))), TypeUint8, 0)
	mustErr(t, Un(OpNeg, Convert(IntLit("1"), "uint8")), ErrOverflow)
	mustErr(t, Un(OpNeg, Convert(IntLit("-128"), "int8")), ErrOverflow)
	wantTypedInt(t, mustEval(t, Un(OpNeg, Convert(IntLit("-127"), "int8"))), TypeInt8, 127)
}

// Strings support only concatenation and comparisons; comparisons use
// byte order.
func TestStringOps(t *testing.T) {
	v := mustEval(t, Bin(OpAdd, StrLit("foo"), StrLit("bar")))
	if v.Kind() != KindString || v.Str() != "foobar" {
		t.Fatalf("got %s, want untyped string \"foobar\"", v)
	}
	wantUntypedBool(t, mustEval(t, Bin(OpLt, StrLit("abc"), StrLit("abd"))), true)
	wantUntypedBool(t, mustEval(t, Bin(OpLt, StrLit("Z"), StrLit("a"))), true) // byte order
	wantUntypedBool(t, mustEval(t, Bin(OpEq, StrLit("x"), StrLit("x"))), true)
	wantUntypedBool(t, mustEval(t, Bin(OpNe, StrLit("x"), StrLit("y"))), true)

	tv := mustEval(t, Bin(OpAdd, Convert(StrLit("a"), "string"), StrLit("b")))
	if tv.Type() != TypeString || tv.Str() != "ab" {
		t.Fatalf("got %s, want string \"ab\"", tv)
	}

	mustErr(t, Bin(OpSub, StrLit("a"), StrLit("b")), ErrIllegalOp)
	mustErr(t, Bin(OpMul, StrLit("a"), IntLit("2")), ErrTypeMismatch)
	mustErr(t, Bin(OpAdd, StrLit("a"), IntLit("1")), ErrTypeMismatch)
	mustErr(t, Bin(OpMod, StrLit("a"), StrLit("b")), ErrIllegalOp)
}

// Shift rules: integer-kind operands only (an integral rational does
// not qualify), count in [0, 1000], typed left shifts must stay
// representable.
func TestShiftRules(t *testing.T) {
	wantUntypedInt(t, mustEval(t, Bin(OpShl, IntLit("1"), IntLit("10"))), "1024")
	wantUntypedInt(t, mustEval(t, Bin(OpShl, IntLit("1"), IntLit("100"))), pow2(100).String())
	// The count 1000 is legal; the result then exceeds 512 bits.
	mustErr(t, Bin(OpShl, IntLit("1"), IntLit("1000")), ErrTooLarge)
	mustErr(t, Bin(OpShl, IntLit("1"), IntLit("1001")), ErrIllegalOp)
	mustErr(t, Bin(OpShl, IntLit("1"), IntLit("-1")), ErrIllegalOp)

	// Rational kind is rejected even when the value is integral.
	mustErr(t, Bin(OpShl, RatLit("2/1"), IntLit("1")), ErrIllegalOp)
	mustErr(t, Bin(OpShl, IntLit("1"), RatLit("2/1")), ErrIllegalOp)

	// Typed left shift must stay representable; no wraparound.
	wantTypedInt(t, mustEval(t, Bin(OpShl,
		Convert(IntLit("1"), "int8"), Convert(IntLit("6"), "int8"))), TypeInt8, 64)
	mustErr(t, Bin(OpShl, Convert(IntLit("1"), "int8"), Convert(IntLit("7"), "int8")), ErrOverflow)
	wantTypedUint(t, mustEval(t, Bin(OpShl,
		Convert(IntLit("1"), "uint8"), Convert(IntLit("7"), "uint8"))), TypeUint8, 128)
	mustErr(t, Bin(OpShl, Convert(IntLit("1"), "uint8"), Convert(IntLit("8"), "uint8")), ErrOverflow)

	// Two typed operands must have identical types.
	mustErr(t, Bin(OpShl, Convert(IntLit("1"), "int8"), Convert(IntLit("1"), "int16")), ErrTypeMismatch)

	// Untyped count converts to the typed left operand's type.
	wantTypedInt(t, mustEval(t, Bin(OpShl, Convert(IntLit("1"), "int8"), IntLit("3"))), TypeInt8, 8)
}

// Untyped integers are limited to 512 bits; exceeding intermediates are
// "constant too large".
func TestInt512BitBoundary(t *testing.T) {
	max := new(big.Int).Sub(pow2(512), big.NewInt(1))
	wantUntypedInt(t, mustEval(t, BigIntLit(max)), max.String())
	wantUntypedInt(t, mustEval(t, Bin(OpShl, IntLit("1"), IntLit("511"))), pow2(511).String())

	mustErr(t, BigIntLit(pow2(512)), ErrTooLarge)
	mustErr(t, Bin(OpShl, IntLit("1"), IntLit("512")), ErrTooLarge)
	mustErr(t, Bin(OpAdd, BigIntLit(pow2(511)), BigIntLit(pow2(511))), ErrTooLarge)
	mustErr(t, Bin(OpAdd, BigIntLit(max), IntLit("1")), ErrTooLarge)
	mustErr(t, Bin(OpMul, BigIntLit(pow2(300)), BigIntLit(pow2(300))), ErrTooLarge)

	// A value at the limit minus itself is fine.
	wantUntypedInt(t, mustEval(t, Bin(OpSub, BigIntLit(max), BigIntLit(max))), "0")
}

// Logical operators evaluate both sides; errors in the branch that a
// short-circuiting evaluator would skip are still reported.
func TestNoShortCircuit(t *testing.T) {
	mustErr(t, Bin(OpLogAnd, BoolLit(false), Bin(OpDiv, IntLit("1"), IntLit("0"))), ErrDivByZero)
	mustErr(t, Bin(OpLogOr, BoolLit(true), Bin(OpSub, StrLit("a"), StrLit("b"))), ErrIllegalOp)
	mustErr(t, Bin(OpLogAnd, Bin(OpDiv, IntLit("1"), IntLit("0")), BoolLit(false)), ErrDivByZero)
	wantUntypedBool(t, mustEval(t, Bin(OpLogAnd, BoolLit(true), BoolLit(false))), false)
	wantUntypedBool(t, mustEval(t, Bin(OpLogOr, BoolLit(false), BoolLit(true))), true)
}

// Within one node: type mismatch > illegal operation > division by zero
// > unrepresentable. Across the tree: left-to-right, children first,
// first error wins.
func TestErrorPriority(t *testing.T) {
	// Mismatch beats illegal operation: kinds differ, so the operator
	// legality is never consulted.
	mustErr(t, Bin(OpAdd, BoolLit(true), IntLit("1")), ErrTypeMismatch)
	mustErr(t, Bin(OpMod, StrLit("a"), IntLit("1")), ErrTypeMismatch)
	// Same kind, unsupported operator: illegal operation.
	mustErr(t, Bin(OpMod, StrLit("a"), StrLit("b")), ErrIllegalOp)

	// Mismatch beats division by zero.
	mustErr(t, Bin(OpDiv, BoolLit(true), IntLit("0")), ErrTypeMismatch)
	// Illegal operation beats division by zero.
	mustErr(t, Bin(OpMod, RatLit("3/2"), IntLit("0")), ErrIllegalOp)
	// Division by zero beats unrepresentable: converting 2^100 to int8
	// would overflow, but the zero divisor is reported first.
	mustErr(t, Bin(OpDiv, BigIntLit(pow2(100)), Convert(IntLit("0"), "int8")), ErrDivByZero)
	// Unrepresentable is last: no higher-priority problem here.
	mustErr(t, Bin(OpDiv, BigIntLit(pow2(100)), Convert(IntLit("1"), "int8")), ErrOverflow)

	// Tree order: the left subtree's division-by-zero is reported even
	// though the right subtree contains a higher-class error (illegal
	// operation).
	mustErr(t, Bin(OpAdd,
		Bin(OpDiv, IntLit("1"), IntLit("0")),
		Bin(OpMod, StrLit("a"), StrLit("b"))), ErrDivByZero)
	// And the reverse order reports the illegal operation.
	mustErr(t, Bin(OpAdd,
		Bin(OpMod, StrLit("a"), StrLit("b")),
		Bin(OpDiv, IntLit("1"), IntLit("0"))), ErrIllegalOp)
}

// Structurally invalid trees are invalid-argument errors and outrank
// every evaluation error, wherever they appear.
func TestStructuralErrors(t *testing.T) {
	// Operator/operand count mismatch, with a division by zero inside.
	bad := &Node{Op: OpAdd, Args: []*Node{Bin(OpDiv, IntLit("1"), IntLit("0"))}}
	mustErr(t, bad, ErrInvalidArgument)

	// Unknown operator.
	mustErr(t, &Node{Op: Op(999), Args: nil}, ErrInvalidArgument)

	// Unknown conversion type name beats the division by zero inside.
	mustErr(t, Convert(Bin(OpDiv, IntLit("1"), IntLit("0")), "int7"), ErrInvalidArgument)

	// Reference to an empty name.
	mustErr(t, Name(""), ErrInvalidArgument)

	// Nil node and nil child.
	mustErr(t, nil, ErrInvalidArgument)
	mustErr(t, Bin(OpAdd, IntLit("1"), nil), ErrInvalidArgument)

	// Literal with inconsistent payload.
	mustErr(t, &Node{Op: OpLit, Lit: Lit{Kind: KindInt}}, ErrInvalidArgument)
}

// Unknown names outrank evaluation errors anywhere in the tree.
func TestUnknownNamePriority(t *testing.T) {
	expr := Bin(OpAdd, Bin(OpDiv, IntLit("1"), IntLit("0")), Name("missing"))
	mustErr(t, expr, ErrUnknownName)
}

// Mixed untyped/typed arithmetic converts the untyped side; failures
// are unrepresentable errors of the matching sub-kind.
func TestMixedUntypedTyped(t *testing.T) {
	wantTypedInt(t, mustEval(t, Bin(OpAdd, Convert(IntLit("5"), "int8"), IntLit("3"))), TypeInt8, 8)
	// Untyped rational to int8: 5/2 is not integral -> truncation.
	mustErr(t, Bin(OpAdd, Convert(IntLit("5"), "int8"), RatLit("5/2")), ErrTruncation)
	// Untyped int 200 to int8: out of range -> overflow.
	mustErr(t, Bin(OpAdd, Convert(IntLit("5"), "int8"), IntLit("200")), ErrOverflow)
	// Untyped int to float64: fine, rounds.
	f := mustEval(t, Bin(OpAdd, Convert(IntLit("1"), "float64"), IntLit("1")))
	if f.Type() != TypeFloat64 || f.Float64() != 2 {
		t.Fatalf("got %s, want float64 2", f)
	}
	// Untyped bool with typed int: no conversion exists.
	mustErr(t, Bin(OpAdd, Convert(IntLit("1"), "int8"), BoolLit(true)), ErrTypeMismatch)
	// Two different typed operands never mix.
	mustErr(t, Bin(OpAdd, Convert(IntLit("1"), "int8"), Convert(IntLit("1"), "int16")), ErrTypeMismatch)
	mustErr(t, Bin(OpAdd, Convert(IntLit("1"), "int8"), Convert(IntLit("1"), "uint8")), ErrTypeMismatch)
}
