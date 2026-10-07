package consteval

import (
	"math"
	"math/big"
)

// nameLookup resolves a constant name to its registered value.
type nameLookup func(string) (Value, bool)

// evalExpr evaluates a structurally valid tree. Children are evaluated left
// to right before their parent (no short-circuiting); the first error in
// that order is returned. Within one node, checks run in priority order:
// type mismatch, illegal operation, division by zero, representability.
func evalExpr(e *Expr, lookup nameLookup) (Value, error) {
	switch e.Node {
	case LitInt:
		i, _ := new(big.Int).SetString(e.Int, 10)
		return mkUntypedInt(i)
	case LitRat:
		r, _ := new(big.Rat).SetString(e.Rat)
		return UntypedRat(r), nil
	case LitBool:
		return UntypedBool(e.Bool), nil
	case LitStr:
		return UntypedString(e.Str), nil
	case Ref:
		v, ok := lookup(e.Name)
		if !ok {
			return Value{}, errf(ErrUnknownName, "constant %q is not registered", e.Name)
		}
		return v, nil
	case Convert:
		x, err := evalExpr(e.Args[0], lookup)
		if err != nil {
			return Value{}, err
		}
		return convert(x, e.Type)
	case Unary:
		x, err := evalExpr(e.Args[0], lookup)
		if err != nil {
			return Value{}, err
		}
		return applyUnary(e.Op, x)
	case Binary:
		l, err := evalExpr(e.Args[0], lookup)
		if err != nil {
			return Value{}, err
		}
		r, err := evalExpr(e.Args[1], lookup)
		if err != nil {
			return Value{}, err
		}
		return applyBinary(e.Op, l, r)
	}
	return Value{}, errf(ErrInvalidArgument, "unknown node kind %d", int(e.Node))
}

// mkUntypedInt wraps an untyped integer result with the 512-bit limit.
func mkUntypedInt(i *big.Int) (Value, error) {
	if err := checkUntypedInt(i); err != nil {
		return Value{}, err
	}
	return Value{typ: NoType, kind: IntKind, i: i}, nil
}

// mkTypedInt wraps a sized-integer result with the range check (no wrap).
func mkTypedInt(t Type, i *big.Int) (Value, error) {
	lo, hi := intRange(t)
	if i.Cmp(lo) < 0 || i.Cmp(hi) > 0 {
		return Value{}, errf(ErrOverflow, "value %s out of range for %s", i.String(), t)
	}
	return Value{typ: t, kind: IntKind, i: i}, nil
}

func isNumeric(k Kind) bool { return k == IntKind || k == RatKind }

func isZero(v Value) bool {
	switch v.kind {
	case IntKind:
		return v.i.Sign() == 0
	case RatKind:
		if v.typ == Float64 {
			return v.f == 0
		}
		return v.r.Sign() == 0
	}
	return false
}

func applyUnary(op Op, x Value) (Value, error) {
	switch op {
	case OpNeg:
		if !isNumeric(x.kind) {
			return Value{}, errf(ErrIllegalOp, "unary - not defined for %s", x.kind)
		}
		switch {
		case x.typ == NoType && x.kind == IntKind:
			return mkUntypedInt(new(big.Int).Neg(x.i))
		case x.typ == NoType:
			return UntypedRat(new(big.Rat).Neg(x.r)), nil
		case x.typ == Float64:
			return Value{typ: Float64, kind: RatKind, f: -x.f}, nil
		case x.typ.IsSigned():
			return mkTypedInt(x.typ, new(big.Int).Neg(x.i))
		default: // unsigned: negating a non-zero value is out of range
			if x.i.Sign() != 0 {
				return Value{}, errf(ErrOverflow, "cannot negate unsigned value %s", x.i)
			}
			return x, nil
		}
	case OpBitNot:
		if x.kind != IntKind {
			return Value{}, errf(ErrIllegalOp, "bitwise complement not defined for %s", x.kind)
		}
		switch {
		case x.typ == NoType, x.typ.IsSigned():
			// ^x == -x-1; always in range for sized signed integers.
			return finishInt(x.typ, new(big.Int).Not(x.i))
		default: // unsigned: bitwise flip within the type's width
			_, hi := intRange(x.typ)
			return Value{typ: x.typ, kind: IntKind, i: new(big.Int).Xor(x.i, hi)}, nil
		}
	case OpNot:
		if x.kind != BoolKind {
			return Value{}, errf(ErrIllegalOp, "logical not not defined for %s", x.kind)
		}
		return Value{typ: x.typ, kind: BoolKind, b: !x.b}, nil
	}
	return Value{}, errf(ErrInvalidArgument, "unknown unary operator %s", op)
}

// finishInt checks an integer result against its domain: the 512-bit limit
// for untyped values, the type range for typed ones.
func finishInt(t Type, i *big.Int) (Value, error) {
	if t == NoType {
		return mkUntypedInt(i)
	}
	return mkTypedInt(t, i)
}

func applyBinary(op Op, l, r Value) (Value, error) {
	// Priority 1: type mismatch. Two typed operands must be identical in
	// type; shifts are exempt because the count's type is irrelevant.
	if op != OpShl && op != OpShr && l.typ != NoType && r.typ != NoType && l.typ != r.typ {
		return Value{}, errf(ErrTypeMismatch, "typed operands %s and %s differ", l.typ, r.typ)
	}
	if l.kind != r.kind && !(isNumeric(l.kind) && isNumeric(r.kind)) {
		return Value{}, errf(ErrTypeMismatch, "cannot combine %s and %s", l.kind, r.kind)
	}

	// Priority 2: illegal operation (including illegal shifts).
	if err := checkOpApplicable(op, l, r); err != nil {
		return Value{}, err
	}

	// Priority 3: division by zero.
	if (op == OpQuo || op == OpRem) && isZero(r) {
		return Value{}, errf(ErrDivByZero, "%s by zero", op)
	}

	// Shifts: the count is never converted and does not affect the result
	// type; only the left operand determines the domain.
	if op == OpShl || op == OpShr {
		return applyShift(op, l, r)
	}

	// Priority 4: representability. Unify the domain, converting an
	// untyped operand to the typed operand's concrete type.
	if l.typ != r.typ {
		var err error
		if l.typ == NoType {
			if l, err = convert(l, r.typ); err != nil {
				return Value{}, err
			}
		} else {
			if r, err = convert(r, l.typ); err != nil {
				return Value{}, err
			}
		}
	}
	return compute(op, l, r)
}

// checkOpApplicable verifies that the operator supports the operand kinds.
func checkOpApplicable(op Op, l, r Value) error {
	illegal := func() error {
		return errf(ErrIllegalOp, "operator %s not defined for %s and %s", op, l.kind, r.kind)
	}
	switch op {
	case OpAdd:
		if isNumeric(l.kind) || l.kind == StringKind {
			return nil
		}
	case OpSub, OpMul, OpQuo:
		if isNumeric(l.kind) {
			return nil
		}
	case OpRem, OpAnd, OpOr, OpXor:
		if l.kind == IntKind && r.kind == IntKind {
			return nil
		}
	case OpShl, OpShr:
		if l.kind == IntKind && r.kind == IntKind {
			return nil
		}
	case OpEql, OpNeq:
		return nil // every kind supports equality within itself
	case OpLss, OpLeq, OpGtr, OpGeq:
		if l.kind != BoolKind {
			return nil
		}
	case OpLAnd, OpLOr:
		if l.kind == BoolKind && r.kind == BoolKind {
			return nil
		}
	}
	return illegal()
}

// applyShift evaluates << and >>. Both operands are of integer kind here.
func applyShift(op Op, l, r Value) (Value, error) {
	n := r.i
	if n.Sign() < 0 || !n.IsInt64() || n.Int64() > maxShiftCount {
		return Value{}, errf(ErrIllegalOp, "illegal shift count %s (need 0..%d)", n, maxShiftCount)
	}
	count := uint(n.Int64())
	if op == OpShl {
		return finishInt(l.typ, new(big.Int).Lsh(l.i, count))
	}
	// Right shift rounds toward negative infinity (arithmetic shift).
	return finishInt(l.typ, new(big.Int).Rsh(l.i, count))
}

// compute evaluates an operator with both operands already in one domain
// (both untyped, or both of one concrete type).
func compute(op Op, l, r Value) (Value, error) {
	if l.typ != NoType {
		return computeTyped(op, l, r)
	}
	// Untyped domain: the wider kind wins (int < rat).
	if l.kind == RatKind || r.kind == RatKind {
		return computeRat(op, l.Rat(), r.Rat())
	}
	switch l.kind {
	case IntKind:
		return computeUntypedInt(op, l.i, r.i)
	case BoolKind:
		return computeBool(op, NoType, l.b, r.b)
	case StringKind:
		return computeString(op, NoType, l.s, r.s)
	}
	return Value{}, errf(ErrInvalidArgument, "bad kind %d", int(l.kind))
}

func computeUntypedInt(op Op, a, b *big.Int) (Value, error) {
	switch op {
	case OpAdd:
		return mkUntypedInt(new(big.Int).Add(a, b))
	case OpSub:
		return mkUntypedInt(new(big.Int).Sub(a, b))
	case OpMul:
		return mkUntypedInt(new(big.Int).Mul(a, b))
	case OpQuo: // truncating integer division
		return mkUntypedInt(new(big.Int).Quo(a, b))
	case OpRem: // truncated remainder (sign of dividend)
		return mkUntypedInt(new(big.Int).Rem(a, b))
	case OpAnd:
		return mkUntypedInt(new(big.Int).And(a, b))
	case OpOr:
		return mkUntypedInt(new(big.Int).Or(a, b))
	case OpXor:
		return mkUntypedInt(new(big.Int).Xor(a, b))
	}
	return compareResult(op, a.Cmp(b)), nil
}

func computeRat(op Op, a, b *big.Rat) (Value, error) {
	switch op {
	case OpAdd:
		return UntypedRat(new(big.Rat).Add(a, b)), nil
	case OpSub:
		return UntypedRat(new(big.Rat).Sub(a, b)), nil
	case OpMul:
		return UntypedRat(new(big.Rat).Mul(a, b)), nil
	case OpQuo: // exact rational division
		return UntypedRat(new(big.Rat).Quo(a, b)), nil
	}
	return compareResult(op, a.Cmp(b)), nil
}

func computeBool(op Op, t Type, a, b bool) (Value, error) {
	mk := func(v bool) Value { return Value{typ: t, kind: BoolKind, b: v} }
	switch op {
	case OpLAnd:
		return mk(a && b), nil
	case OpLOr:
		return mk(a || b), nil
	case OpEql:
		return UntypedBool(a == b), nil
	case OpNeq:
		return UntypedBool(a != b), nil
	}
	return Value{}, errf(ErrIllegalOp, "operator not defined for bool")
}

func computeString(op Op, t Type, a, b string) (Value, error) {
	if op == OpAdd {
		return Value{typ: t, kind: StringKind, s: a + b}, nil
	}
	return compareResult(op, compareStrings(a, b)), nil
}

// compareResult builds an untyped boolean comparison result.
func compareResult(op Op, cmp int) Value {
	var v bool
	switch op {
	case OpEql:
		v = cmp == 0
	case OpNeq:
		v = cmp != 0
	case OpLss:
		v = cmp < 0
	case OpLeq:
		v = cmp <= 0
	case OpGtr:
		v = cmp > 0
	case OpGeq:
		v = cmp >= 0
	}
	return UntypedBool(v)
}

// computeTyped evaluates with both operands of one concrete type; every
// result must be representable in that type (no wraparound).
func computeTyped(op Op, l, r Value) (Value, error) {
	t := l.typ
	switch {
	case t.IsInt():
		return computeTypedInt(op, t, l.i, r.i)
	case t == Float64:
		return computeFloat(op, l.f, r.f)
	case t == Bool:
		return computeBool(op, t, l.b, r.b)
	case t == String:
		return computeString(op, t, l.s, r.s)
	}
	return Value{}, errf(ErrInvalidArgument, "bad type %d", int(t))
}

func computeTypedInt(op Op, t Type, a, b *big.Int) (Value, error) {
	switch op {
	case OpAdd:
		return mkTypedInt(t, new(big.Int).Add(a, b))
	case OpSub:
		return mkTypedInt(t, new(big.Int).Sub(a, b))
	case OpMul:
		return mkTypedInt(t, new(big.Int).Mul(a, b))
	case OpQuo:
		return mkTypedInt(t, new(big.Int).Quo(a, b))
	case OpRem:
		return mkTypedInt(t, new(big.Int).Rem(a, b))
	case OpAnd:
		return mkTypedInt(t, new(big.Int).And(a, b))
	case OpOr:
		return mkTypedInt(t, new(big.Int).Or(a, b))
	case OpXor:
		return mkTypedInt(t, new(big.Int).Xor(a, b))
	}
	return compareResult(op, a.Cmp(b)), nil
}

func computeFloat(op Op, a, b float64) (Value, error) {
	var f float64
	switch op {
	case OpAdd:
		f = a + b
	case OpSub:
		f = a - b
	case OpMul:
		f = a * b
	case OpQuo:
		f = a / b
	default:
		return compareResult(op, compareFloats(a, b)), nil
	}
	if math.IsInf(f, 0) {
		return Value{}, errf(ErrOverflow, "float64 result overflows to infinity")
	}
	return Value{typ: Float64, kind: RatKind, f: f}, nil
}

func compareFloats(a, b float64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
