package constexpr

import (
	"math/big"
	"testing"
)

func over512() *big.Int { return new(big.Int).Lsh(big.NewInt(1), 600) }

// 无类型任意精度中间值最终落回范围内 vs 有类型每步越界。
func TestUntypedPrecisionVsTypedStep(t *testing.T) {
	bigV := bi("9000000000")
	untyped := Binary(OpSub,
		Binary(OpAdd, IntLit(bigV), IntLit(bigV)),
		IntLit(bigV))
	v := mustEval(t, untyped)
	if v.Kind() != KindInt || v.Int().Cmp(bigV) != 0 {
		t.Fatalf("无类型结果错误: %v", v)
	}
	v2 := mustEval(t, Conv("i64", untyped))
	if !v2.IsTyped() || v2.Type() != CTypeI64 {
		t.Fatalf("转换 i64 失败: %v", v2)
	}
	typedAdd := Binary(OpAdd,
		Conv("i32", IntLit(bigV)),
		Conv("i32", IntLit(bigV)))
	wantCode(t, typedAdd, EvalOverflow)
	wantCode(t, Conv("i32", untyped), EvalOverflow)
}

func TestDivisionKindSelection(t *testing.T) {
	v := mustEval(t, Binary(OpDiv, IntLit(bi("7")), IntLit(bi("2"))))
	if v.Kind() != KindInt || v.Int().Int64() != 3 {
		t.Fatalf("整除应为整数 3，得到 %s", v.Kind())
	}
	v = mustEval(t, Binary(OpDiv, IntLit(bi("-7")), IntLit(bi("2"))))
	if v.Int().Int64() != -3 {
		t.Fatalf("向零截断 -7/2 应为 -3，得到 %s", v.Int())
	}
	v = mustEval(t, Binary(OpDiv, IntLit(bi("7")), RatLit(br("2"))))
	if v.Kind() != KindRat || v.Rat().Cmp(br("7/2")) != 0 {
		t.Fatalf("精确除法应为 7/2 有理数，得到 %v", v)
	}
	v = mustEval(t, Binary(OpDiv, RatLit(br("6")), RatLit(br("3"))))
	if v.Kind() != KindRat || v.Rat().Cmp(br("2")) != 0 {
		t.Fatalf("6/3 应保留有理数种类，得到 %v", v)
	}
	wantCode(t, Binary(OpDiv, IntLit(bi("1")), IntLit(bi("0"))), EvalDivideByZero)
	wantCode(t, Binary(OpDiv, RatLit(br("1")), IntLit(bi("0"))), EvalDivideByZero)
}

func TestRemainderAndShr(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{7, 3, 1}, {-7, 3, -1}, {7, -3, 1}, {-7, -3, -1},
	}
	for _, c := range cases {
		v := mustEval(t, Binary(OpRem, IntLit(big.NewInt(c.a)), IntLit(big.NewInt(c.b))))
		if v.Int().Int64() != c.want {
			t.Fatalf("%d %% %d = %d, 期望 %d", c.a, c.b, v.Int(), c.want)
		}
	}
	v := mustEval(t, Binary(OpShr, IntLit(bi("-7")), IntLit(bi("1"))))
	if v.Int().Int64() != -4 {
		t.Fatalf("-7>>1 应为 -4，得到 %s", v.Int())
	}
	v = mustEval(t, Binary(OpShr, IntLit(bi("7")), IntLit(bi("1"))))
	if v.Int().Int64() != 3 {
		t.Fatalf("7>>1 应为 3，得到 %s", v.Int())
	}
	v = mustEval(t, Binary(OpShr, Conv("i8", IntLit(bi("-128"))), IntLit(bi("1"))))
	if v.Int().Int64() != -64 {
		t.Fatalf("i8 -128>>1 应为 -64，得到 %s", v.Int())
	}
	wantCode(t, Binary(OpShl, Conv("i8", IntLit(bi("1"))), IntLit(bi("7"))), EvalOverflow)
	v = mustEval(t, Binary(OpShl, IntLit(bi("1")), IntLit(bi("500"))))
	if v.Int().BitLen() != 501 {
		t.Fatalf("1<<500 位宽应为 501，得到 %d", v.Int().BitLen())
	}
	wantCode(t, Binary(OpShl, IntLit(bi("1")), IntLit(bi("1001"))), EvalIllegalOp)
	wantCode(t, Binary(OpShl, IntLit(bi("1")), IntLit(bi("-1"))), EvalIllegalOp)
	wantCode(t, Binary(OpShl, IntLit(bi("1")), RatLit(br("3/2"))), EvalIllegalOp)
	wantCode(t, Binary(OpShl, RatLit(br("4")), IntLit(bi("1"))), EvalIllegalOp)
	v = mustEval(t, Binary(OpShl, IntLit(bi("1")), RatLit(br("3"))))
	if v.Int().Int64() != 8 {
		t.Fatalf("1<<(rat 3) 应为 8，得到 %s", v.Int())
	}
}
