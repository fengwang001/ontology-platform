package ontology

import (
	"math/rand"
	"testing"
)

// randomValue 为给定规则生成随机合法值。
func randomValue(r *rand.Rand, rule string) Value {
	switch rule {
	case RuleMaxInt, RuleMinInt:
		return int64(r.Intn(20))
	case RuleSetUnion:
		n := r.Intn(4)
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, string(rune('a'+r.Intn(6))))
		}
		return out
	case RuleLWW:
		return LWWValue{
			Clock:  uint64(r.Intn(5)),
			Writer: string(rune('w' + rune(r.Intn(3)))),
			Data:   string(rune('x' + rune(r.Intn(4)))),
		}
	default:
		panic("unknown rule")
	}
}

// TestRuleSemilatticeAxioms 对所有内置规则随机验证半格三公理：
// 交换律、结合律、幂等律。满足三公理才能保证合并结果与到达顺序无关。
func TestRuleSemilatticeAxioms(t *testing.T) {
	for _, name := range []string{RuleMaxInt, RuleMinInt, RuleSetUnion, RuleLWW} {
		rule := RuleByName(name)
		if rule == nil {
			t.Fatalf("规则 %s 未注册", name)
		}
		r := rand.New(rand.NewSource(42))
		for i := 0; i < 2000; i++ {
			// 半格公理在规范形定义域上成立：写入路径会先 Canonical。
			a := rule.Canonical(randomValue(r, name))
			b := rule.Canonical(randomValue(r, name))
			c := rule.Canonical(randomValue(r, name))
			if !Equal(rule.Join(a, b), rule.Join(b, a)) {
				t.Fatalf("%s 违反交换律: a=%v b=%v", name, a, b)
			}
			if !Equal(rule.Join(rule.Join(a, b), c), rule.Join(a, rule.Join(b, c))) {
				t.Fatalf("%s 违反结合律: a=%v b=%v c=%v", name, a, b, c)
			}
			if !Equal(rule.Join(a, a), a) {
				t.Fatalf("%s 违反幂等律: a=%v", name, a)
			}
		}
	}
}

// TestLWWDeterministicTieBreak 验证 LWW 决胜只依赖写入内容：
// 时钟相同按写入者、再按数据排序，与 Join 参数顺序无关。
func TestLWWDeterministicTieBreak(t *testing.T) {
	rule := RuleByName(RuleLWW)
	a := LWWValue{Clock: 7, Writer: "alice", Data: "v1"}
	b := LWWValue{Clock: 7, Writer: "bob", Data: "v2"}
	// 时钟相同，bob > alice，bob 胜出，与顺序无关。
	if got := rule.Join(a, b); got != b {
		t.Fatalf("Join(a,b)=%v, 期望 %v", got, b)
	}
	if got := rule.Join(b, a); got != b {
		t.Fatalf("Join(b,a)=%v, 期望 %v", got, b)
	}
	// 时钟更高者胜出。
	c := LWWValue{Clock: 8, Writer: "alice", Data: "v3"}
	if got := rule.Join(b, c); got != c {
		t.Fatalf("Join(b,c)=%v, 期望 %v", got, c)
	}
}
