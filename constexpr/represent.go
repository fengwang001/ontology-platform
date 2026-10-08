package constexpr

import (
	"math"
	"math/big"
)

// MaxUntypedIntBits 是无类型整数允许的最大绝对位宽。
const MaxUntypedIntBits = 512

// checkUntypedIntBits 检查无类型整数是否超过 512 位限制。
func checkUntypedIntBits(z *big.Int) error {
	if z.BitLen() > MaxUntypedIntBits {
		return errf(EvalConstantTooLarge, "无类型整数 %s 的位宽 %d 超过 %d 位限制", z.String(), z.BitLen(), MaxUntypedIntBits)
	}
	return nil
}

// representableIn 判断值在目标类型下是否可表示。
// 返回 ErrTruncation（不是整数）与 ErrOverflow（范围外/舍入到无穷）以区分。
func representableIn(v *Value, t CType) error {
	switch t {
	case CTypeF64:
		if v.kind != KindInt && v.kind != KindRat {
			return errf(EvalIllegalOp, "种类 %s 不能转换到 f64", v.kind)
		}
		f := ratToFloat64(v.asRat())
		if math.IsInf(f, 0) {
			return errf(EvalOverflow, "数值 %s 舍入到双精度后为无穷", numericString(v))
		}
		return nil
	case CTypeBool:
		if v.kind != KindBool {
			return errf(EvalIllegalOp, "种类 %s 不能转换到 bool", v.kind)
		}
		return nil
	case CTypeString:
		if v.kind != KindString {
			return errf(EvalIllegalOp, "种类 %s 不能转换到 string", v.kind)
		}
		return nil
	default:
		if !t.IsInt() {
			return errf(EvalInvalidArgument, "内部错误：未知目标类型 %s", t)
		}
		if v.kind != KindInt && v.kind != KindRat {
			return errf(EvalIllegalOp, "种类 %s 不能转换到整型", v.kind)
		}
		r := v.asRat()
		if !r.IsInt() {
			return errf(EvalTruncation, "值 %s 不是整数，转换到 %s 会截断", r.RatString(), t)
		}
		lo, hi := t.intRange()
		num := r.Num()
		if num.Cmp(lo) < 0 || num.Cmp(hi) > 0 {
			return errf(EvalOverflow, "值 %s 超出 %s 范围 [%s, %s]", num.String(), t, lo.String(), hi.String())
		}
		return nil
	}
}

// ratToFloat64 按“最近偶数”把有理数舍入为双精度浮点。
func ratToFloat64(r *big.Rat) float64 {
	// 以远高于尾数位宽的精度经大浮点中转，ToNearestEven 给出
	// IEEE-754 最近偶数舍入；量级超出双精度范围时结果为 ±Inf。
	f, _ := new(big.Float).SetPrec(4096).SetMode(big.ToNearestEven).SetRat(r).Float64()
	return f
}

// convertValue 把值转换为目标类型并产出新 Value（载荷不变式）。
func convertValue(v *Value, t CType) (*Value, error) {
	if err := representableIn(v, t); err != nil {
		return nil, err
	}
	switch t {
	case CTypeF64:
		return typedRat(v.asRat()), nil
	case CTypeBool:
		return typedBool(v.b), nil
	case CTypeString:
		return typedString(v.s), nil
	default:
		return typedInt(v.asRat().Num(), t), nil
	}
}

// defaultType 返回需要具体类型的语境下各种类的默认类型。
func defaultType(k Kind) CType {
	switch k {
	case KindInt:
		return CTypeI64
	case KindRat:
		return CTypeF64
	case KindBool:
		return CTypeBool
	case KindString:
		return CTypeString
	}
	return CTypeInvalid
}

func numericString(v *Value) string {
	if v.kind == KindInt {
		return v.i.String()
	}
	return v.r.RatString()
}
