package constexpr

// 本文件实现独立“朴素模型”：不经过求值器，直接用 math/big.Int /
// math/big.Rat 按同一套规则重新算一遍，再与求值器随机对拍。

import (
	"math"
	"math/big"
)

type mval struct {
	kind Kind
	t    CType // CTypeInvalid 表示无类型
	i    *big.Int
	r    *big.Rat
	b    bool
	s    string
}

func mInt(i *big.Int, t CType) *mval  { return &mval{kind: KindInt, t: t, i: new(big.Int).Set(i)} }
func mRat(r *big.Rat, t CType) *mval  { return &mval{kind: KindRat, t: t, r: new(big.Rat).Set(r)} }
func mBool(b bool, t CType) *mval     { return &mval{kind: KindBool, t: t, b: b} }
func mString(s string, t CType) *mval { return &mval{kind: KindString, t: t, s: s} }

func mErr(code EvalCode) error { return errf(code, "naive") }

func mCoerce(a, b *mval) (*mval, *mval, CType, error) {
	at, bt := a.t != CTypeInvalid, b.t != CTypeInvalid
	switch {
	case at && bt:
		if a.t != b.t {
			return nil, nil, CTypeInvalid, mErr(EvalTypeMismatch)
		}
		return a, b, a.t, nil
	case at:
		cb, err := mConvert(b, a.t)
		if err != nil {
			return nil, nil, CTypeInvalid, err
		}
		return a, cb, a.t, nil
	case bt:
		ca, err := mConvert(a, b.t)
		if err != nil {
			return nil, nil, CTypeInvalid, err
		}
		return ca, b, b.t, nil
	default:
		return a, b, CTypeInvalid, nil
	}
}

func mConvert(v *mval, t CType) (*mval, error) {
	if err := mRepresentable(v, t); err != nil {
		return nil, err
	}
	switch t {
	case CTypeF64:
		return mRat(v.asRat(), CTypeF64), nil
	case CTypeBool:
		return mBool(v.b, CTypeBool), nil
	case CTypeString:
		return mString(v.s, CTypeString), nil
	default:
		return mInt(v.asRat().Num(), t), nil
	}
}

func (v *mval) asRat() *big.Rat {
	if v.kind == KindInt {
		return new(big.Rat).SetInt(v.i)
	}
	return new(big.Rat).Set(v.r)
}

func mRepresentable(v *mval, t CType) error {
	switch t {
	case CTypeF64:
		if v.kind != KindInt && v.kind != KindRat {
			return mErr(EvalIllegalOp)
		}
		if isInfRat(v.asRat()) {
			return mErr(EvalOverflow)
		}
		return nil
	case CTypeBool:
		if v.kind != KindBool {
			return mErr(EvalIllegalOp)
		}
		return nil
	case CTypeString:
		if v.kind != KindString {
			return mErr(EvalIllegalOp)
		}
		return nil
	}
	if v.kind != KindInt && v.kind != KindRat {
		return mErr(EvalIllegalOp)
	}
	r := v.asRat()
	if !r.IsInt() {
		return mErr(EvalTruncation)
	}
	lo, hi := t.intRange()
	if r.Num().Cmp(lo) < 0 || r.Num().Cmp(hi) > 0 {
		return mErr(EvalOverflow)
	}
	return nil
}

// isInfRat 用 float64 舍入判断是否溢出为无穷（与生产代码同一舍入规则）。
func isInfRat(r *big.Rat) bool {
	return isInfFloat(ratToFloat64(r))
}

func isInfFloat(f float64) bool { return math.IsInf(f, 0) }

func mFinishInt(z *big.Int, t CType) (*mval, error) {
	if t != CTypeInvalid {
		if err := mRepresentable(mInt(z, CTypeInvalid), t); err != nil {
			return nil, err
		}
		return mInt(z, t), nil
	}
	if z.BitLen() > MaxUntypedIntBits {
		return nil, mErr(EvalConstantTooLarge)
	}
	return mInt(z, CTypeInvalid), nil
}

func mFinishRat(z *big.Rat, t CType) (*mval, error) {
	if t == CTypeF64 {
		if err := mRepresentable(mRat(z, CTypeInvalid), CTypeF64); err != nil {
			return nil, err
		}
		return mRat(z, CTypeF64), nil
	}
	if t != CTypeInvalid {
		return nil, mErr(EvalTruncation)
	}
	return mRat(z, CTypeInvalid), nil
}
