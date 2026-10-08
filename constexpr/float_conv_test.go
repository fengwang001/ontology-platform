package constexpr

import (
	"math"
	"math/big"
	"testing"
)

func TestFloatRoundingTiesToEven(t *testing.T) {
	base := new(big.Int).Lsh(big.NewInt(1), 53)
	// tie: 2^53+1 位于偶数 2^53 与 2^53+2 正中间，取偶 -> 2^53。
	tie := new(big.Rat).SetInt(new(big.Int).Add(base, big.NewInt(1)))
	if got := ratToFloat64(tie); got != float64(9007199254740992) {
		t.Fatalf("tie 2^53+1 应舍到偶数 2^53，得到 %v", got)
	}
	if got := ratToFloat64(new(big.Rat).SetInt(new(big.Int).Add(base, big.NewInt(2)))); got != 9007199254740994.0 {
		t.Fatalf("2^53+2 应精确，得到 %v", got)
	}
	if ratToFloat64(br("1/2")) != 0.5 {
		t.Fatal("1/2 != 0.5")
	}
	huge := new(big.Rat).SetInt(new(big.Int).Lsh(big.NewInt(1), 2000))
	if !math.IsInf(ratToFloat64(huge), 1) {
		t.Fatalf("极大值应舍入为 +Inf")
	}
	// 无类型整数受 512 位限制；用无界有理数构造舍入到无穷的场景。
	wantCode(t, Conv("f64", RatLit(huge)), EvalOverflow)
	// f64 逐步运算保留精确分数载荷：(1/3)*3 == 1。
	e := Binary(OpMul,
		Conv("f64", RatLit(br("1/3"))),
		Conv("f64", IntLit(bi("3"))))
	v := mustEval(t, e)
	if v.Rat().Cmp(br("1")) != 0 {
		t.Fatalf("f64 (1/3)*3 精确载荷应为 1，得到 %v", v.Rat())
	}
}

func TestConversionBoundaries(t *testing.T) {
	mustOK := func(s string, typ string) {
		t.Helper()
		mustEval(t, Conv(typ, IntLit(bi(s))))
	}
	mustOK("-128", "i8")
	mustOK("127", "i8")
	wantCode(t, Conv("i8", IntLit(bi("-129"))), EvalOverflow)
	wantCode(t, Conv("i8", IntLit(bi("128"))), EvalOverflow)
	mustOK("0", "u8")
	mustOK("255", "u8")
	wantCode(t, Conv("u8", IntLit(bi("-1"))), EvalOverflow)
	wantCode(t, Conv("u8", IntLit(bi("256"))), EvalOverflow)
	wantCode(t, Conv("i8", RatLit(br("3/2"))), EvalTruncation)
	// 值恰为整数但超界 -> 越界；非整数（哪怕整数部分超界）-> 截断。
	wantCode(t, Conv("i8", RatLit(br("1000"))), EvalOverflow)
	wantCode(t, Conv("i8", RatLit(br("1000/3"))), EvalTruncation)
	wantCode(t, Conv("i8", RatLit(br("5/2"))), EvalTruncation)
	wantCode(t, Conv("i8", BoolLit(true)), EvalIllegalOp)
	wantCode(t, Conv("f64", StringLit("x")), EvalIllegalOp)
	wantCode(t, Conv("bool", IntLit(bi("1"))), EvalIllegalOp)
}

func Test512BitBoundary(t *testing.T) {
	limit := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	over := new(big.Int).Lsh(big.NewInt(1), 512)
	mustEval(t, IntLit(limit))
	wantCode(t, IntLit(over), EvalConstantTooLarge)
	half := new(big.Int).Lsh(big.NewInt(1), 511)
	wantCode(t, Binary(OpMul, IntLit(half), IntLit(bi("2"))), EvalConstantTooLarge)
	wantCode(t, IntLit(new(big.Int).Neg(over)), EvalConstantTooLarge)
	wantCode(t, Binary(OpAdd, IntLit(limit), IntLit(bi("1"))), EvalConstantTooLarge)
}
