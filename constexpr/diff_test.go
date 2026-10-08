package constexpr

import (
	"fmt"
	"math/rand"
	"testing"
)

func codeOf(err error) EvalCode {
	if err == nil {
		return EvalOK
	}
	if ee, ok := err.(*EvalError); ok {
		return ee.Code
	}
	return EvalInvalidArgument
}

func sameValue(v *Value, m *mval) bool {
	if v == nil || m == nil {
		return v == nil && m == nil
	}
	if v.Kind() != m.kind || v.Type() != m.t {
		return false
	}
	switch v.Kind() {
	case KindInt:
		return v.Int().Cmp(m.i) == 0
	case KindRat:
		return v.Rat().Cmp(m.r) == 0
	case KindBool:
		return v.Bool() == m.b
	case KindString:
		return v.String() == m.s
	}
	return false
}

func describeV(v *Value) string {
	if v == nil {
		return "<nil>"
	}
	switch v.Kind() {
	case KindInt:
		return fmt.Sprintf("(kind=int type=%s val=%s)", v.Type(), v.Int().String())
	case KindRat:
		return fmt.Sprintf("(kind=rat type=%s val=%s)", v.Type(), v.Rat().RatString())
	case KindBool:
		return fmt.Sprintf("(kind=bool type=%s val=%v)", v.Type(), v.Bool())
	case KindString:
		return fmt.Sprintf("(kind=string type=%s val=%q)", v.Type(), v.String())
	}
	return "?"
}

func describeM(m *mval) string {
	if m == nil {
		return "<nil>"
	}
	switch m.kind {
	case KindInt:
		return fmt.Sprintf("(kind=int type=%s val=%s)", m.t, m.i.String())
	case KindRat:
		return fmt.Sprintf("(kind=rat type=%s val=%s)", m.t, m.r.RatString())
	case KindBool:
		return fmt.Sprintf("(kind=bool type=%s val=%v)", m.t, m.b)
	case KindString:
		return fmt.Sprintf("(kind=string type=%s val=%q)", m.t, m.s)
	}
	return "?"
}

// TestRandomDifferential 与独立朴素模型对拍大量随机无类型表达式。
func TestRandomDifferential(t *testing.T) {
	const n = 4000
	for iter := 0; iter < n; iter++ {
		seed := int64(0xC0FFEE + iter)
		g := &gen{rng: rand.New(rand.NewSource(seed)), depth: 4}
		e := g.genExpr(false)

		// 生产实现：挂载 tracer 记录每个节点的输入/输出/判定依据。
		var trace []string
		ev := NewEvaluator(WithTracer(func(ev TraceEvent) {
			trace = append(trace, fmt.Sprintf("    node=%s err=%v", opName(ev.Op), ev.Err))
		}))
		v, err := ev.Eval(e)

		m, merr := mEval(e, map[string]*mval{})

		gotCode, wantCode2 := codeOf(err), codeOf(merr)
		header := fmt.Sprintf("iter=%d seed=%d\n  输入: %s", iter, seed, e.String())
		if gotCode != wantCode2 {
			t.Fatalf("%s\n  错误码不一致: 实现=%s 朴素=%s\n  实现err=%v 朴素err=%v",
				header, gotCode, wantCode2, err, merr)
		}
		if gotCode == EvalOK && !sameValue(v, m) {
			t.Fatalf("%s\n  值不一致:\n    实现=%s\n    朴素=%s",
				header, describeV(v), describeM(m))
		}
		// 打印每次输入、输出与判定依据（go test -v 可见）。
		if gotCode == EvalOK {
			t.Logf("%s\n  输出: %s\n  判定: 两侧均成功且值相等；节点数=%d",
				header, describeV(v), len(trace))
		} else {
			t.Logf("%s\n  输出: 错误 %s\n  判定: 两侧独立得出相同错误分类", header, gotCode)
		}
	}
}
