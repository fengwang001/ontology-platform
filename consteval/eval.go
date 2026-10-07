package consteval

import (
	"math"
	"math/big"
	"strings"
)

const (
	// maxIntBits bounds untyped integers: |x| must fit in 512 bits.
	maxIntBits = 512
	// maxShiftCount bounds shift counts.
	maxShiftCount = 1000
)

// nameLookup resolves a constant name; the registry supplies it with
// the appropriate lock already held.
type nameLookup func(string) (*Const, bool)

// evalNode evaluates a structurally valid tree. Children are evaluated
// left to right before their parent, and the first error encountered in
// that order is returned. Within a node, checks run in priority order:
// type mismatch, illegal operation, division by zero, unrepresentable.
func evalNode(n *Node, lookup nameLookup) (*Const, *Error) {
	switch n.Op {
	case OpLit:
		return evalLit(&n.Lit)
	case OpName:
		c, ok := lookup(n.Name)
		if !ok {
			return nil, errf(ErrUnknownName, "unknown constant %q", n.Name)
		}
		return c, nil
	case OpConvert:
		v, err := evalNode(n.Args[0], lookup)
		if err != nil {
			return nil, err
		}
		t, _ := ParseType(n.Type)
		return convertTo(v, t)
	case OpNeg, OpBitNot, OpNot:
		a, err := evalNode(n.Args[0], lookup)
		if err != nil {
			return nil, err
		}
		return evalUnary(n.Op, a)
	default:
		l, err := evalNode(n.Args[0], lookup)
		if err != nil {
			return nil, err
		}
		r, err := evalNode(n.Args[1], lookup)
		if err != nil {
			return nil, err
		}
		return evalBinary(n.Op, l, r)
	}
}

func evalLit(l *Lit) (*Const, *Error) {
	switch l.Kind {
	case KindInt:
		v := new(big.Int).Set(l.Int)
		if err := checkIntSize(v); err != nil {
			return nil, err
		}
		return untypedInt(v), nil
	case KindRational:
		return untypedRat(new(big.Rat).Set(l.Rat)), nil
	case KindBool:
		return untypedBool(l.Bool), nil
	case KindString:
		return untypedString(l.Str), nil
	}
	return nil, errf(ErrInvalidArgument, "literal with invalid kind %d", int(l.Kind))
}

// checkIntSize enforces the 512-bit limit on untyped integers.
func checkIntSize(v *big.Int) *Error {
	if v.BitLen() > maxIntBits {
		return errf(ErrTooLarge, "untyped integer %s exceeds %d bits", v, maxIntBits)
	}
	return nil
}

// ---------------------------------------------------------------------------
// unary operators
// ---------------------------------------------------------------------------

func evalUnary(op Op, a *Const) (*Const, *Error) {
	switch op {
	case OpNeg:
		return evalNeg(a)
	case OpBitNot:
		return evalBitNot(a)
	case OpNot:
		if a.kind != KindBool {
			return nil, errf(ErrIllegalOp, "operator ! is not defined for %s", a)
		}
		// Logical operators yield untyped bool constants.
		return untypedBool(!a.b), nil
	}
	return nil, errf(ErrInvalidArgument, "unknown unary operator %d", int(op))
}

func evalNeg(a *Const) (*Const, *Error) {
	switch a.category() {
	case catInt:
		if a.typ == TypeNone {
			v := new(big.Int).Neg(a.i)
			if err := checkIntSize(v); err != nil {
				return nil, err
			}
			return untypedInt(v), nil
		}
		if a.typ.Signed() {
			v := new(big.Int).Neg(big.NewInt(a.si))
			min, max := intRange(a.typ)
			if v.Cmp(min) < 0 || v.Cmp(max) > 0 {
				return nil, errf(ErrOverflow, "negation of %s overflows %s", a, a.typ)
			}
			return typedSigned(a.typ, v.Int64()), nil
		}
		// Negating a nonzero unsigned value leaves its range.
		if a.ui != 0 {
			return nil, errf(ErrOverflow, "negation of %s is out of range for %s", a, a.typ)
		}
		return typedUnsigned(a.typ, 0), nil
	case catRat:
		return untypedRat(new(big.Rat).Neg(a.r)), nil
	case catFloat:
		return typedFloat(-a.f), nil
	}
	return nil, errf(ErrIllegalOp, "negation is not defined for %s", a)
}

func evalBitNot(a *Const) (*Const, *Error) {
	switch a.category() {
	case catInt:
		if a.typ == TypeNone {
			// Untyped: ^x == -x - 1.
			v := new(big.Int).Not(a.i)
			if err := checkIntSize(v); err != nil {
				return nil, err
			}
			return untypedInt(v), nil
		}
		if a.typ.Signed() {
			// -x - 1 always fits the same signed width.
			return typedSigned(a.typ, -a.si-1), nil
		}
		// Unsigned: bitwise complement within the type's width.
		mask := uint64(math.MaxUint64)
		if w := uint(a.typ.Bits()); w < 64 {
			mask = 1<<w - 1
		}
		return typedUnsigned(a.typ, (^a.ui)&mask), nil
	}
	return nil, errf(ErrIllegalOp, "operator ^ is not defined for %s", a)
}

// ---------------------------------------------------------------------------
// binary operators
// ---------------------------------------------------------------------------

// evalBinary evaluates a binary operator on already-evaluated operands,
// applying the per-node error priority: type mismatch, illegal
// operation, division by zero, then unrepresentable results.
func evalBinary(op Op, l, r *Const) (*Const, *Error) {
	if err := checkCompatible(l, r); err != nil {
		return nil, err
	}
	if err := checkLegalBinary(op, l, r); err != nil {
		return nil, err
	}
	if (op == OpDiv || op == OpMod) && r.isZero() {
		return nil, errf(ErrDivByZero, "division by zero in %s %s %s", l, opSymbol[op], r)
	}
	ul, ur, err := unify(l, r)
	if err != nil {
		return nil, err
	}
	return computeBinary(op, ul, ur)
}

// checkCompatible enforces the operand-combination rules:
//   - two typed operands must have identical types;
//   - an untyped operand mixed with a typed one must be convertible to
//     that type's category;
//   - two untyped operands must share a kind, except that int and
//     rational mix (widening to rational).
func checkCompatible(l, r *Const) *Error {
	if l.typ != TypeNone && r.typ != TypeNone {
		if l.typ != r.typ {
			return errf(ErrTypeMismatch, "typed operands have different types %s and %s", l.typ, r.typ)
		}
		return nil
	}
	if l.typ == TypeNone && r.typ == TypeNone {
		if l.kind == r.kind {
			return nil
		}
		if (l.kind == KindInt || l.kind == KindRational) && (r.kind == KindInt || r.kind == KindRational) {
			return nil
		}
		return errf(ErrTypeMismatch, "cannot mix untyped %s with untyped %s", l.kind, r.kind)
	}
	t, u := l, r
	if t.typ == TypeNone {
		t, u = r, l
	}
	switch u.kind {
	case KindInt, KindRational:
		if t.typ.IsInt() || t.typ == TypeFloat64 {
			return nil
		}
	case KindBool:
		if t.typ == TypeBool {
			return nil
		}
	case KindString:
		if t.typ == TypeString {
			return nil
		}
	}
	return errf(ErrTypeMismatch, "cannot mix untyped %s with %s", u.kind, t.typ)
}

// checkLegalBinary enforces operator/kind legality, including all shift
// restrictions. It runs after compatibility, so operand categories are
// known to be combinable.
func checkLegalBinary(op Op, l, r *Const) *Error {
	ca, cb := l.category(), r.category()
	switch op {
	case OpAdd:
		if numericCat(ca) && numericCat(cb) {
			return nil
		}
		if ca == catString && cb == catString {
			return nil
		}
	case OpSub, OpMul, OpDiv:
		if numericCat(ca) && numericCat(cb) {
			return nil
		}
	case OpMod, OpBitAnd, OpBitOr, OpBitXor:
		if ca == catInt && cb == catInt {
			return nil
		}
	case OpShl, OpShr:
		// Both operands must be of integer kind; a rational whose value
		// happens to be integral does not qualify. The count must be a
		// non-negative integer no larger than maxShiftCount.
		if ca != catInt || cb != catInt {
			return errf(ErrIllegalOp, "illegal shift: operands of %s %s %s must be integers",
				l, opSymbol[op], r)
		}
		count := r.intValue()
		if count.Sign() < 0 || count.Cmp(big.NewInt(maxShiftCount)) > 0 {
			return errf(ErrIllegalOp, "illegal shift count %s (must be in [0, %d])",
				count, maxShiftCount)
		}
		return nil
	case OpEq, OpNe:
		return nil
	case OpLt, OpLe, OpGt, OpGe:
		if numericCat(ca) && numericCat(cb) {
			return nil
		}
		if ca == catString && cb == catString {
			return nil
		}
	case OpLogAnd, OpLogOr:
		if ca == catBool && cb == catBool {
			return nil
		}
	default:
		return errf(ErrInvalidArgument, "unknown binary operator %d", int(op))
	}
	return errf(ErrIllegalOp, "operator %s is not defined for %s and %s", opSymbol[op], l, r)
}

// unify brings both operands into a common representation:
//   - untyped int mixed with untyped rational widens to rational;
//   - an untyped operand mixed with a typed one converts to that type
//     (must be representable);
//   - identical typed operands pass through.
func unify(l, r *Const) (*Const, *Const, *Error) {
	if l.typ == TypeNone && r.typ == TypeNone {
		switch {
		case l.kind == KindInt && r.kind == KindRational:
			return untypedRat(new(big.Rat).SetInt(l.i)), r, nil
		case l.kind == KindRational && r.kind == KindInt:
			return l, untypedRat(new(big.Rat).SetInt(r.i)), nil
		}
		return l, r, nil
	}
	if l.typ != TypeNone && r.typ == TypeNone {
		rc, err := convertTo(r, l.typ)
		if err != nil {
			return nil, nil, err
		}
		return l, rc, nil
	}
	if l.typ == TypeNone && r.typ != TypeNone {
		lc, err := convertTo(l, r.typ)
		if err != nil {
			return nil, nil, err
		}
		return lc, r, nil
	}
	return l, r, nil
}

// computeBinary evaluates op on unified operands and enforces
// representability of typed results (no wraparound) and the 512-bit
// limit on untyped integers.
func computeBinary(op Op, l, r *Const) (*Const, *Error) {
	if l.typ == TypeNone {
		switch l.kind {
		case KindInt:
			return intBinary(op, l.i, r.i)
		case KindRational:
			return ratBinary(op, l.r, r.r)
		case KindBool:
			return boolBinary(op, l.b, r.b)
		case KindString:
			return stringBinary(op, l.s, r.s, TypeNone)
		}
	}
	switch {
	case l.typ.IsInt() && l.typ.Signed():
		return signedBinary(op, l.typ, l.si, r.si)
	case l.typ.IsInt():
		return unsignedBinary(op, l.typ, l.ui, r.ui)
	case l.typ == TypeFloat64:
		return floatBinary(op, l.f, r.f)
	case l.typ == TypeBool:
		return boolBinary(op, l.b, r.b)
	case l.typ == TypeString:
		return stringBinary(op, l.s, r.s, l.typ)
	}
	return nil, errf(ErrInvalidArgument, "cannot compute %s %d %s", l, int(op), r)
}

func intBinary(op Op, x, y *big.Int) (*Const, *Error) {
	z := new(big.Int)
	switch op {
	case OpAdd:
		z.Add(x, y)
	case OpSub:
		z.Sub(x, y)
	case OpMul:
		z.Mul(x, y)
	case OpDiv:
		// Truncated toward zero.
		z.Quo(x, y)
	case OpMod:
		// Truncated remainder; the sign follows the dividend.
		z.Rem(x, y)
	case OpBitAnd:
		z.And(x, y)
	case OpBitOr:
		z.Or(x, y)
	case OpBitXor:
		z.Xor(x, y)
	case OpShl:
		z.Lsh(x, uint(y.Uint64()))
	case OpShr:
		// Arithmetic shift: rounds toward negative infinity.
		z.Rsh(x, uint(y.Uint64()))
	default:
		return untypedBool(compareBigInts(op, x.Cmp(y))), nil
	}
	if err := checkIntSize(z); err != nil {
		return nil, err
	}
	return untypedInt(z), nil
}

func ratBinary(op Op, x, y *big.Rat) (*Const, *Error) {
	z := new(big.Rat)
	switch op {
	case OpAdd:
		z.Add(x, y)
	case OpSub:
		z.Sub(x, y)
	case OpMul:
		z.Mul(x, y)
	case OpDiv:
		// Exact division, no rounding.
		z.Quo(x, y)
	default:
		return untypedBool(compareBigInts(op, x.Cmp(y))), nil
	}
	return untypedRat(z), nil
}

func boolBinary(op Op, x, y bool) (*Const, *Error) {
	switch op {
	case OpLogAnd:
		return untypedBool(x && y), nil
	case OpLogOr:
		return untypedBool(x || y), nil
	case OpEq:
		return untypedBool(x == y), nil
	case OpNe:
		return untypedBool(x != y), nil
	}
	return nil, errf(ErrIllegalOp, "operator %s is not defined for bool", opSymbol[op])
}

func stringBinary(op Op, x, y string, t Type) (*Const, *Error) {
	if op == OpAdd {
		if t == TypeNone {
			return untypedString(x + y), nil
		}
		return typedString(x + y), nil
	}
	return untypedBool(compareBigInts(op, strings.Compare(x, y))), nil
}

// compareBigInts maps a three-way comparison and a comparison operator
// to a boolean result.
func compareBigInts(op Op, cmp int) bool {
	switch op {
	case OpEq:
		return cmp == 0
	case OpNe:
		return cmp != 0
	case OpLt:
		return cmp < 0
	case OpLe:
		return cmp <= 0
	case OpGt:
		return cmp > 0
	case OpGe:
		return cmp >= 0
	}
	return false
}

func signedBinary(op Op, t Type, x, y int64) (*Const, *Error) {
	switch op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		cmp := 0
		if x < y {
			cmp = -1
		} else if x > y {
			cmp = 1
		}
		return untypedBool(compareBigInts(op, cmp)), nil
	}
	bx, by := big.NewInt(x), big.NewInt(y)
	z := new(big.Int)
	switch op {
	case OpAdd:
		z.Add(bx, by)
	case OpSub:
		z.Sub(bx, by)
	case OpMul:
		z.Mul(bx, by)
	case OpDiv:
		z.Quo(bx, by)
	case OpMod:
		z.Rem(bx, by)
	case OpBitAnd:
		z.And(bx, by)
	case OpBitOr:
		z.Or(bx, by)
	case OpBitXor:
		z.Xor(bx, by)
	case OpShl:
		z.Lsh(bx, uint(by.Uint64()))
	case OpShr:
		// Arithmetic shift: rounds toward negative infinity.
		z.Rsh(bx, uint(by.Uint64()))
	}
	min, max := intRange(t)
	if z.Cmp(min) < 0 || z.Cmp(max) > 0 {
		return nil, errf(ErrOverflow, "result %s is out of range for %s", z, t)
	}
	return typedSigned(t, z.Int64()), nil
}

func unsignedBinary(op Op, t Type, x, y uint64) (*Const, *Error) {
	switch op {
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		cmp := 0
		if x < y {
			cmp = -1
		} else if x > y {
			cmp = 1
		}
		return untypedBool(compareBigInts(op, cmp)), nil
	}
	bx := new(big.Int).SetUint64(x)
	by := new(big.Int).SetUint64(y)
	z := new(big.Int)
	switch op {
	case OpAdd:
		z.Add(bx, by)
	case OpSub:
		z.Sub(bx, by)
	case OpMul:
		z.Mul(bx, by)
	case OpDiv:
		z.Quo(bx, by)
	case OpMod:
		z.Rem(bx, by)
	case OpBitAnd:
		z.And(bx, by)
	case OpBitOr:
		z.Or(bx, by)
	case OpBitXor:
		z.Xor(bx, by)
	case OpShl:
		z.Lsh(bx, uint(by.Uint64()))
	case OpShr:
		z.Rsh(bx, uint(by.Uint64()))
	}
	_, max := intRange(t)
	if z.Sign() < 0 || z.Cmp(max) > 0 {
		return nil, errf(ErrOverflow, "result %s is out of range for %s", z, t)
	}
	return typedUnsigned(t, z.Uint64()), nil
}

func floatBinary(op Op, x, y float64) (*Const, *Error) {
	switch op {
	case OpEq:
		return untypedBool(x == y), nil
	case OpNe:
		return untypedBool(x != y), nil
	case OpLt:
		return untypedBool(x < y), nil
	case OpLe:
		return untypedBool(x <= y), nil
	case OpGt:
		return untypedBool(x > y), nil
	case OpGe:
		return untypedBool(x >= y), nil
	}
	var z float64
	switch op {
	case OpAdd:
		z = x + y
	case OpSub:
		z = x - y
	case OpMul:
		z = x * y
	case OpDiv:
		z = x / y
	}
	// Operands are always finite, so an infinite result means overflow;
	// NaN cannot arise (division by zero is rejected earlier).
	if math.IsInf(z, 0) {
		return nil, errf(ErrOverflow, "float64 result of %v %s %v overflows", x, opSymbol[op], y)
	}
	return typedFloat(z), nil
}
