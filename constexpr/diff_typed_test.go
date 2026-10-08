package constexpr

import (
	"math/big"
	"math/rand"
	"testing"
)

// TestRandomTypedDifferential 对拍“小整数 + i64 包裹”的有类型表达式，
// 覆盖逐步可表示性检查与类型一致检查。
func TestRandomTypedDifferential(t *testing.T) {
	const n = 2000
	for iter := 0; iter < n; iter++ {
		rng := rand.New(rand.NewSource(int64(0xBEEF + iter)))
		e := buildTyped(rng, 3)

		gotEvents := 0
		var first TraceEvent
		ev := NewEvaluator(WithTracer(func(ev TraceEvent) {
			if gotEvents == 0 {
				first = ev
			}
			gotEvents++
		}))
		v, err := ev.Eval(e)
		m, merr := mEval(e, map[string]*mval{})

		gc, mc := codeOf(err), codeOf(merr)
		if gc != mc {
			t.Fatalf("iter=%d 输入=%s\n错误码不一致: 实现=%s 朴素=%s (%v vs %v)",
				iter, e.String(), gc, mc, err, merr)
		}
		if gc == EvalOK && !sameValue(v, m) {
			t.Fatalf("iter=%d 输入=%s\n值不一致: 实现=%s 朴素=%s",
				iter, e.String(), describeV(v), describeM(m))
		}
		if gotEvents == 0 {
			t.Fatalf("iter=%d tracer 未记录任何节点", iter)
		}
		verdict := "两侧均成功且值相等"
		if gc != EvalOK {
			verdict = "两侧独立得出相同错误分类 " + gc.String()
		}
		t.Logf("iter=%d\n  输入: %s\n  输出: %s / err=%s\n  判定依据: tracer 记录 %d 个节点；首节点=%s；%s",
			iter, e.String(), describeV(v), gc, gotEvents, opName(first.Op), verdict)
	}
}

// buildTyped 生成全部数值叶子都被 i64 包裹、布尔叶子被 bool 包裹的树，
// 深度受限，数值取小值以降低频繁越界，但仍保留一定越界/除零分布。
func buildTyped(rng *rand.Rand, depth int) *Expr {
	if depth <= 0 || rng.Intn(3) == 0 {
		if rng.Intn(2) == 0 {
			return Conv("bool", BoolLit(rng.Intn(2) == 0))
		}
		x := rng.Int63n(64) - 32
		return Conv("i64", IntLit(bigInt(x)))
	}
	depth--
	switch rng.Intn(8) {
	case 0:
		return Unary(OpNeg, buildTyped(rng, depth))
	case 1:
		return Unary(OpBitNot, buildTyped(rng, depth))
	case 2:
		return Unary(OpLogNot, buildBoolTyped(rng, depth))
	case 3:
		return Binary([]Op{OpShl, OpShr}[rng.Intn(2)],
			buildTyped(rng, depth),
			IntLit(bigInt(int64(rng.Intn(63)))))
	case 4:
		op := []Op{OpEq, OpNe, OpLt, OpLe, OpGt, OpGe}[rng.Intn(6)]
		return Binary(op, buildTyped(rng, depth), buildTyped(rng, depth))
	case 5:
		op := []Op{OpLogAnd, OpLogOr}[rng.Intn(2)]
		return Binary(op, buildBoolTyped(rng, depth), buildBoolTyped(rng, depth))
	default:
		op := []Op{OpAdd, OpSub, OpMul, OpDiv, OpRem, OpBitAnd, OpBitOr, OpBitXor}[rng.Intn(8)]
		left := buildTyped(rng, depth)
		right := buildTyped(rng, depth)
		if op == OpDiv || op == OpRem {
			right = Conv("i64", IntLit(bigInt(int64(rng.Intn(5)+1))))
		}
		return Binary(op, left, right)
	}
}

func buildBoolTyped(rng *rand.Rand, depth int) *Expr {
	if depth <= 0 || rng.Intn(2) == 0 {
		return Conv("bool", BoolLit(rng.Intn(2) == 0))
	}
	return Binary([]Op{OpLogAnd, OpLogOr}[rng.Intn(2)],
		buildBoolTyped(rng, depth-1), buildBoolTyped(rng, depth-1))
}

func bigInt(x int64) *big.Int { return big.NewInt(x) }
