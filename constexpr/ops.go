package constexpr

import "math/big"

// coercePair 处理两个操作数的类型组合：
//  1. 两个有类型操作数类型必须完全一致，否则类型不匹配（最高优先级）；
//  2. 仅有一方有类型时，无类型一方转换到该类型（可表示性错误最后报告）；
//  3. 两个无类型操作数原样返回。
func coercePair(a, b *Value) (*Value, *Value, CType, error) {
	at, bt := a.IsTyped(), b.IsTyped()
	switch {
	case at && bt:
		if a.ctype != b.ctype {
			return nil, nil, CTypeInvalid, errf(EvalTypeMismatch,
				"有类型操作数类型不一致：%s 与 %s", a.ctype, b.ctype)
		}
		return a, b, a.ctype, nil
	case at && !bt:
		cb, err := convertValue(b, a.ctype)
		if err != nil {
			return nil, nil, CTypeInvalid, err
		}
		return a, cb, a.ctype, nil
	case !at && bt:
		ca, err := convertValue(a, b.ctype)
		if err != nil {
			return nil, nil, CTypeInvalid, err
		}
		return ca, b, b.ctype, nil
	default:
		return a, b, CTypeInvalid, nil
	}
}

func (ev *Evaluator) evalArith(op Op, av, bv *Value) (*Value, error) {
	a, b, t, err := coercePair(av, bv)
	if err != nil {
		return nil, err
	}

	// 字符串只支持加法。
	if a.kind == KindString || b.kind == KindString {
		if a.kind != KindString || b.kind != KindString || op != OpAdd {
			return nil, errf(EvalIllegalOp, "字符串种类只支持加法与比较，不能做 %s", opName(op))
		}
		return makeStringValue(a.s+b.s, t), nil
	}

	// 布尔不能参与算术。
	if a.kind == KindBool || b.kind == KindBool {
		return nil, errf(EvalIllegalOp, "布尔种类不能做 %s", opName(op))
	}

	// 取余与位运算只允许整数种类（非法操作优先于除零）。
	intOnly := op == OpRem || op == OpBitAnd || op == OpBitOr || op == OpBitXor
	if intOnly && (a.kind != KindInt || b.kind != KindInt) {
		return nil, errf(EvalIllegalOp, "%s 只允许整数种类操作数", opName(op))
	}

	// 除法：两个整数种类向零截断整除；任一方是有理数种类则精确除法。
	if op == OpDiv && b.asRat().Sign() == 0 {
		return nil, errf(EvalDivideByZero, "除数为零")
	}
	if op == OpRem && b.i.Sign() == 0 {
		return nil, errf(EvalDivideByZero, "取余除数为零")
	}

	bothInt := a.kind == KindInt && b.kind == KindInt
	if bothInt {
		z := new(big.Int)
		switch op {
		case OpAdd:
			z.Add(a.i, b.i)
		case OpSub:
			z.Sub(a.i, b.i)
		case OpMul:
			z.Mul(a.i, b.i)
		case OpDiv:
			z.Quo(a.i, b.i) // 向零截断
		case OpRem:
			z.Rem(a.i, b.i) // 与 Quo 配套的截断取余
		case OpBitAnd:
			z.And(a.i, b.i)
		case OpBitOr:
			z.Or(a.i, b.i)
		case OpBitXor:
			z.Xor(a.i, b.i)
		default:
			return nil, errf(EvalIllegalOp, "整数种类不支持 %s", opName(op))
		}
		return finishInt(z, t)
	}

	// 有理数精确运算路径。
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
		return nil, errf(EvalIllegalOp, "有理数种类不支持 %s", opName(op))
	}
	return finishRat(z, t)
}

func makeStringValue(s string, t CType) *Value {
	if t == CTypeString {
		return typedString(s)
	}
	return untypedString(s)
}

// finishInt 把整数结果落实为有类型（逐步可表示）或无类型（512 位限制）值。
func finishInt(z *big.Int, t CType) (*Value, error) {
	if t != CTypeInvalid {
		if err := representableIn(untypedInt(z), t); err != nil {
			return nil, err
		}
		return typedInt(z, t), nil
	}
	if err := checkUntypedIntBits(z); err != nil {
		return nil, err
	}
	return untypedInt(z), nil
}

// finishRat 把有理数结果落实类型；有类型语境只能是 f64。
func finishRat(z *big.Rat, t CType) (*Value, error) {
	if t == CTypeF64 {
		if err := representableIn(untypedRat(z), CTypeF64); err != nil {
			return nil, err
		}
		return typedRat(z), nil
	}
	if t != CTypeInvalid {
		// 整型语境下含有理数操作数的情形已在 coercePair 转换阶段报截断。
		return nil, errf(EvalTruncation, "有理数结果不能表示为 %s", t)
	}
	return untypedRat(z), nil
}

func (ev *Evaluator) evalUnary(op Op, av *Value) (*Value, error) {
	switch op {
	case OpLogNot:
		if av.kind != KindBool {
			return nil, errf(EvalIllegalOp, "逻辑非只允许布尔种类")
		}
		return makeBoolValue(!av.b, av.ctype), nil
	case OpBitNot:
		if av.kind != KindInt {
			return nil, errf(EvalIllegalOp, "按位取反只允许整数种类")
		}
		if av.IsTyped() && !av.ctype.Signed() {
			// 无符号：在其位宽内翻转。
			w := uint(av.ctype.BitWidth())
			mask := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), w), big.NewInt(1))
			z := new(big.Int).Xor(av.i, mask)
			return typedInt(z, av.ctype), nil
		}
		// 无类型整数与有符号整数：^x == -x-1。
		z := new(big.Int).Neg(av.i)
		z.Sub(z, big.NewInt(1))
		return finishInt(z, av.ctype)
	case OpNeg:
		if av.kind == KindBool || av.kind == KindString {
			return nil, errf(EvalIllegalOp, "%s 种类不能取负", av.kind)
		}
		if av.IsTyped() && av.ctype.IsInt() && !av.ctype.Signed() {
			// 对无符号整数取负：仅零合法，非零必越界（无回绕）。
			if av.i.Sign() != 0 {
				return nil, errf(EvalOverflow, "无符号值 %s 取负越界", av.i.String())
			}
			return typedInt(big.NewInt(0), av.ctype), nil
		}
		if av.kind == KindInt {
			z := new(big.Int).Neg(av.i)
			return finishInt(z, av.ctype)
		}
		z := new(big.Rat).Neg(av.r)
		return finishRat(z, av.ctype)
	}
	return nil, errf(EvalIllegalOp, "未知一元运算 %s", opName(op))
}

func makeBoolValue(b bool, t CType) *Value {
	if t == CTypeBool {
		return typedBool(b)
	}
	return untypedBool(b)
}

func (ev *Evaluator) evalLogic(op Op, av, bv *Value) (*Value, error) {
	a, b, t, err := coercePair(av, bv)
	if err != nil {
		return nil, err
	}
	if a.kind != KindBool || b.kind != KindBool {
		return nil, errf(EvalIllegalOp, "逻辑运算只允许布尔种类")
	}
	// 两侧此时均已求值，错误不会被短路吞掉。
	r := false
	if op == OpLogAnd {
		r = a.b && b.b
	} else {
		r = a.b || b.b
	}
	return makeBoolValue(r, t), nil
}

func (ev *Evaluator) evalCompare(op Op, av, bv *Value) (*Value, error) {
	a, b, _, err := coercePair(av, bv)
	if err != nil {
		return nil, err
	}
	if a.kind != b.kind {
		return nil, errf(EvalIllegalOp, "不能比较不同种类：%s 与 %s", a.kind, b.kind)
	}
	switch a.kind {
	case KindString:
		c := compareStrings(a.s, b.s)
		return untypedBool(compareResult(op, c)), nil
	case KindBool:
		if op != OpEq && op != OpNe {
			return nil, errf(EvalIllegalOp, "布尔只支持相等比较，不能做 %s", opName(op))
		}
		c := 0
		switch {
		case a.b == b.b:
			c = 0
		case !a.b:
			c = -1
		default:
			c = 1
		}
		return untypedBool(compareResult(op, c)), nil
	case KindInt:
		return untypedBool(compareResult(op, a.i.Cmp(b.i))), nil
	default: // KindRat（含 f64 精确载荷）
		return untypedBool(compareResult(op, a.asRat().Cmp(b.asRat()))), nil
	}
}

// compareStrings 按字节序（即 Go 原生字符串序）比较。
func compareStrings(a, b string) int {
	switch {
	case a == b:
		return 0
	case a < b:
		return -1
	default:
		return 1
	}
}

func compareResult(op Op, c int) bool {
	switch op {
	case OpEq:
		return c == 0
	case OpNe:
		return c != 0
	case OpLt:
		return c < 0
	case OpLe:
		return c <= 0
	case OpGt:
		return c > 0
	case OpGe:
		return c >= 0
	}
	return false
}

func (ev *Evaluator) evalShift(op Op, av, cv *Value) (*Value, error) {
	// 左操作数必须是整数种类；值为整数的有理数不算。
	if av.kind != KindInt {
		return nil, errf(EvalIllegalOp, "移位左操作数必须是整数种类")
	}
	// 右操作数按值要求：非负整数且不超过 1000。
	rr := cv.asRat()
	if !rr.IsInt() {
		return nil, errf(EvalIllegalOp, "非法移位：移位量不是整数")
	}
	n := rr.Num()
	if n.Sign() < 0 {
		return nil, errf(EvalIllegalOp, "非法移位：移位量为负数 %s", n.String())
	}
	if n.Cmp(big.NewInt(1000)) > 0 {
		return nil, errf(EvalIllegalOp, "非法移位：移位量 %s 超过 1000", n.String())
	}
	k := uint(n.Uint64())

	if op == OpShl {
		z := new(big.Int).Lsh(av.i, k)
		return finishInt(z, av.ctype)
	}
	// 算术右移：向负无穷取整 z = floor(x / 2^k)。
	z := floorDivPow2(av.i, k)
	return finishInt(z, av.ctype)
}

// floorDivPow2 返回 floor(x / 2^k)，对负数即算术右移。
func floorDivPow2(x *big.Int, k uint) *big.Int {
	div := new(big.Int).Lsh(big.NewInt(1), k)
	q, rem := new(big.Int).QuoRem(x, div, new(big.Int))
	// QuoRem 向零截断；余数与 x 同号，非整除且 x<0 时再减一。
	if rem.Sign() != 0 && x.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
	}
	return q
}
