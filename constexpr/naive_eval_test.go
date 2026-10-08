package constexpr

import "math/big"

// mEval 用朴素模型对表达式树求值；env 提供命名常量。
func mEval(e *Expr, env map[string]*mval) (*mval, error) {
	subs := make([]*mval, len(e.Operands))
	for i, s := range e.Operands {
		v, err := mEval(s, env)
		if err != nil {
			return nil, err
		}
		subs[i] = v
	}
	switch e.Op {
	case OpLit:
		switch e.LitKind {
		case KindInt:
			if e.IntVal.BitLen() > MaxUntypedIntBits {
				return nil, mErr(EvalConstantTooLarge)
			}
			return mInt(e.IntVal, CTypeInvalid), nil
		case KindRat:
			return mRat(e.RatVal, CTypeInvalid), nil
		case KindBool:
			return mBool(e.BoolVal, CTypeInvalid), nil
		case KindString:
			return mString(e.StrVal, CTypeInvalid), nil
		}
	case OpRef:
		v, ok := env[e.Name]
		if !ok {
			return nil, mErr(EvalUnknownName)
		}
		return v, nil
	case OpConv:
		return mConvert(subs[0], e.Type)
	case OpDefaultConv:
		if subs[0].t != CTypeInvalid {
			return subs[0], nil
		}
		return mConvert(subs[0], defaultType(subs[0].kind))
	}
	switch e.Op {
	case OpNeg, OpBitNot, OpLogNot:
		return mUnary(e.Op, subs[0])
	case OpLogAnd, OpLogOr:
		a, b, t, err := mCoerce(subs[0], subs[1])
		if err != nil {
			return nil, err
		}
		if a.kind != KindBool || b.kind != KindBool {
			return nil, mErr(EvalIllegalOp)
		}
		r := a.b && b.b
		if e.Op == OpLogOr {
			r = a.b || b.b
		}
		return mBool(r, t), nil
	case OpShl, OpShr:
		return mShift(e.Op, subs[0], subs[1])
	case OpEq, OpNe, OpLt, OpLe, OpGt, OpGe:
		return mCompare(e.Op, subs[0], subs[1])
	}
	return mArith(e.Op, subs[0], subs[1])
}

func mArith(op Op, av, bv *mval) (*mval, error) {
	a, b, t, err := mCoerce(av, bv)
	if err != nil {
		return nil, err
	}
	if a.kind == KindString || b.kind == KindString {
		if a.kind != KindString || b.kind != KindString || op != OpAdd {
			return nil, mErr(EvalIllegalOp)
		}
		return mString(a.s+b.s, t), nil
	}
	if a.kind == KindBool || b.kind == KindBool {
		return nil, mErr(EvalIllegalOp)
	}
	intOnly := op == OpRem || op == OpBitAnd || op == OpBitOr || op == OpBitXor
	if intOnly && (a.kind != KindInt || b.kind != KindInt) {
		return nil, mErr(EvalIllegalOp)
	}
	if op == OpDiv && b.asRat().Sign() == 0 {
		return nil, mErr(EvalDivideByZero)
	}
	if op == OpRem && b.i.Sign() == 0 {
		return nil, mErr(EvalDivideByZero)
	}
	if a.kind == KindInt && b.kind == KindInt {
		z := new(big.Int)
		switch op {
		case OpAdd:
			z.Add(a.i, b.i)
		case OpSub:
			z.Sub(a.i, b.i)
		case OpMul:
			z.Mul(a.i, b.i)
		case OpDiv:
			z.Quo(a.i, b.i)
		case OpRem:
			z.Rem(a.i, b.i)
		case OpBitAnd:
			z.And(a.i, b.i)
		case OpBitOr:
			z.Or(a.i, b.i)
		case OpBitXor:
			z.Xor(a.i, b.i)
		default:
			return nil, mErr(EvalIllegalOp)
		}
		return mFinishInt(z, t)
	}
	ra, rb := a.asRat(), b.asRat()
	z := new(big.Rat)
	switch op {
	case OpAdd:
		z.Add(ra, rb)
	case OpSub:
		z.Sub(ra, rb)
	case OpMul:
		z.Mul(ra, rb)
	case OpDiv:
		z.Quo(ra, rb)
	default:
		return nil, mErr(EvalIllegalOp)
	}
	return mFinishRat(z, t)
}

func mUnary(op Op, a *mval) (*mval, error) {
	switch op {
	case OpLogNot:
		if a.kind != KindBool {
			return nil, mErr(EvalIllegalOp)
		}
		return mBool(!a.b, a.t), nil
	case OpBitNot:
		if a.kind != KindInt {
			return nil, mErr(EvalIllegalOp)
		}
		if a.t != CTypeInvalid && !a.t.Signed() {
			w := uint(a.t.BitWidth())
			mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), w), big.NewInt(1))
			return mInt(new(big.Int).Xor(a.i, mask), a.t), nil
		}
		z := new(big.Int).Sub(new(big.Int).Neg(a.i), big.NewInt(1))
		return mFinishInt(z, a.t)
	case OpNeg:
		if a.kind == KindBool || a.kind == KindString {
			return nil, mErr(EvalIllegalOp)
		}
		if a.t != CTypeInvalid && a.t.IsInt() && !a.t.Signed() {
			if a.i.Sign() != 0 {
				return nil, mErr(EvalOverflow)
			}
			return mInt(big.NewInt(0), a.t), nil
		}
		if a.kind == KindInt {
			return mFinishInt(new(big.Int).Neg(a.i), a.t)
		}
		return mFinishRat(new(big.Rat).Neg(a.r), a.t)
	}
	return nil, mErr(EvalIllegalOp)
}

func mCompare(op Op, av, bv *mval) (*mval, error) {
	a, b, _, err := mCoerce(av, bv)
	if err != nil {
		return nil, err
	}
	if a.kind != b.kind {
		return nil, mErr(EvalIllegalOp)
	}
	var c int
	switch a.kind {
	case KindString:
		c = compareStrings(a.s, b.s)
	case KindBool:
		if op != OpEq && op != OpNe {
			return nil, mErr(EvalIllegalOp)
		}
		switch {
		case a.b == b.b:
			c = 0
		case !a.b:
			c = -1
		default:
			c = 1
		}
	case KindInt:
		c = a.i.Cmp(b.i)
	default:
		c = a.asRat().Cmp(b.asRat())
	}
	return mBool(compareResult(op, c), CTypeInvalid), nil
}

func mShift(op Op, av, cv *mval) (*mval, error) {
	if av.kind != KindInt {
		return nil, mErr(EvalIllegalOp)
	}
	rr := cv.asRat()
	if !rr.IsInt() {
		return nil, mErr(EvalIllegalOp)
	}
	n := rr.Num()
	if n.Sign() < 0 || n.Cmp(big.NewInt(1000)) > 0 {
		return nil, mErr(EvalIllegalOp)
	}
	k := uint(n.Uint64())
	if op == OpShl {
		return mFinishInt(new(big.Int).Lsh(av.i, k), av.t)
	}
	return mFinishInt(floorDivPow2(av.i, k), av.t)
}
