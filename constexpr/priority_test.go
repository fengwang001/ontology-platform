package constexpr

import "testing"

func TestNoShortCircuit(t *testing.T) {
	eq := Binary(OpEq,
		Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0"))),
		IntLit(bi("0")))
	wantCode(t, Binary(OpLogOr, BoolLit(true), eq), EvalDivideByZero)
	wantCode(t, Binary(OpLogAnd, BoolLit(false), eq), EvalDivideByZero)
}

func TestErrorPriority(t *testing.T) {
	// 类型不匹配优先于除零。
	e := Binary(OpDiv,
		Conv("i32", IntLit(bi("1"))),
		Conv("i64", IntLit(bi("0"))))
	wantCode(t, e, EvalTypeMismatch)
	// 非法操作（有理数取余）优先于除零。
	wantCode(t, Binary(OpRem, RatLit(br("1/2")), IntLit(bi("0"))), EvalIllegalOp)
	// 除零优先于不可表示：类型一致、操作合法但除数为零。
	wantCode(t, Binary(OpDiv,
		Conv("i8", IntLit(bi("1"))),
		Conv("i8", IntLit(bi("0")))), EvalDivideByZero)
	// 先子后父、从左到右：先报左侧除零。
	lr := Binary(OpAdd,
		Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0"))),
		IntLit(over512()))
	wantCode(t, lr, EvalDivideByZero)
	// 右侧错误晚于左侧：调换次序则报常量过大。
	rl := Binary(OpAdd,
		IntLit(over512()),
		Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0"))))
	wantCode(t, rl, EvalConstantTooLarge)
}

func TestUnarySemantics(t *testing.T) {
	v := mustEval(t, Unary(OpBitNot, IntLit(bi("0"))))
	if v.Int().Int64() != -1 {
		t.Fatalf("^0 应为 -1，得到 %s", v.Int())
	}
	v = mustEval(t, Unary(OpBitNot, Conv("i8", IntLit(bi("0")))))
	if v.Int().Int64() != -1 {
		t.Fatalf("i8 ^0 应为 -1，得到 %s", v.Int())
	}
	v = mustEval(t, Unary(OpBitNot, Conv("u8", IntLit(bi("0")))))
	if v.Int().Int64() != 255 {
		t.Fatalf("u8 ^0 应为 255，得到 %s", v.Int())
	}
	v = mustEval(t, Unary(OpNeg, Conv("u8", IntLit(bi("0")))))
	if v.Int().Int64() != 0 {
		t.Fatalf("u8 -0 应为 0，得到 %s", v.Int())
	}
	wantCode(t, Unary(OpNeg, Conv("u8", IntLit(bi("1")))), EvalOverflow)
	// 有符号最小负数取负（-(-128)=128）也越界。
	wantCode(t, Unary(OpNeg, Conv("i8", IntLit(bi("-128")))), EvalOverflow)
	// 有理数种类取反精确保留。
	v = mustEval(t, Unary(OpNeg, RatLit(br("3/2"))))
	if v.Kind() != KindRat || v.Rat().Cmp(br("-3/2")) != 0 {
		t.Fatalf("-3/2 取反应为 -3/2 有理数，得到 %v", v)
	}
}

func TestStringSemantics(t *testing.T) {
	v := mustEval(t, Binary(OpAdd, StringLit("abc"), StringLit("def")))
	if v.String() != "abcdef" {
		t.Fatalf("字符串相加错误: %q", v.String())
	}
	v = mustEval(t, Binary(OpLt, StringLit("abc"), StringLit("abd")))
	if !v.Bool() {
		t.Fatalf("字节序比较错误")
	}
	v = mustEval(t, Binary(OpLt, StringLit("B"), StringLit("a")))
	if !v.Bool() {
		t.Fatalf("字节序应 B < a")
	}
	wantCode(t, Binary(OpSub, StringLit("a"), StringLit("b")), EvalIllegalOp)
	// 无类型字符串与有类型整型相加：无类型串无法转整型，属非法操作。
	wantCode(t, Binary(OpAdd, StringLit("a"), Conv("i32", IntLit(bi("1")))), EvalIllegalOp)
	// 真正的类型不匹配：两个有类型但类型不同（优先于“字符串只支持加法”）。
	wantCode(t, Binary(OpAdd, Conv("string", StringLit("a")), Conv("i32", IntLit(bi("1")))), EvalTypeMismatch)
}

func TestComparisonMixed(t *testing.T) {
	// 两个有类型操作数比较，结果仍是无类型布尔。
	cmp := Binary(OpLt, Conv("i32", IntLit(bi("1"))), Conv("i32", IntLit(bi("2"))))
	v := mustEval(t, Binary(OpLogAnd, cmp, BoolLit(true)))
	if !v.Bool() {
		t.Fatalf("有类型比较结果应是无类型 true")
	}
	wantCode(t, Binary(OpLt, Conv("i32", IntLit(bi("1"))), Conv("i64", IntLit(bi("2")))), EvalTypeMismatch)
	// 无类型 vs 有类型，无类型超界则报越界。
	wantCode(t, Binary(OpLt, IntLit(bi("9999999999")), Conv("i32", IntLit(bi("1")))), EvalOverflow)
	// 布尔只支持相等比较。
	wantCode(t, Binary(OpLt, BoolLit(true), BoolLit(false)), EvalIllegalOp)
	mustEval(t, Binary(OpNe, BoolLit(true), BoolLit(false)))
}
