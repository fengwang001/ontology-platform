package naive_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/ontology"
	"ontology/ontology/naive"
)

// 随机生成策略集合与实例，将主引擎结果与朴素参照实现逐项对照。
// 两边以不同顺序登记同一批策略，同时验证结果与登记顺序无关。
func TestDifferentialRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(20261007))
	attrs := []string{"a0", "a1", "a2", "a3", "a4"}
	subjects := []string{"alice", "bob", "carol"}

	for iter := 0; iter < 3000; iter++ {
		schema := map[string]ontology.AttrType{}
		for _, a := range attrs {
			switch rng.Intn(3) {
			case 0:
				schema[a] = ontology.AttrType{Kind: ontology.KindString, MaxLen: 1 + rng.Intn(12)}
			case 1:
				schema[a] = ontology.AttrType{Kind: ontology.KindInt, HasRange: true,
					Min: 0, Max: int64(rng.Intn(100))}
			case 2:
				schema[a] = ontology.AttrType{Kind: ontology.KindBool}
			}
		}

		inst := ontology.Instance{ID: "inst", Attrs: map[string]ontology.Value{}}
		for _, a := range attrs {
			if rng.Intn(10) < 8 {
				inst.Attrs[a] = randValue(rng, schema[a])
			}
		}
		if rng.Intn(10) < 2 {
			inst.Attrs["ghost"] = ontology.StringValue("boo") // 未声明属性
		}

		var vis []ontology.VisibilityPolicy
		var mask []ontology.MaskingPolicy
		for i := 0; i < rng.Intn(10); i++ {
			p := ontology.VisibilityPolicy{
				ID:      visID(i, rng),
				Subject: subjects[rng.Intn(len(subjects)+1)%len(subjects)],
				Attr:    attrs[rng.Intn(len(attrs))],
				Effect:  ontology.VisibilityEffect(rng.Intn(2)),
			}
			if rng.Intn(2) == 0 {
				p.Subject = "*"
			}
			if rng.Intn(3) > 0 {
				p.Cond = &ontology.Condition{
					Attr:  attrs[rng.Intn(len(attrs))],
					Op:    ontology.CondOp(rng.Intn(2)),
					Value: randAnyValue(rng),
				}
			}
			vis = append(vis, p)
		}
		for i := 0; i < rng.Intn(10); i++ {
			p := ontology.MaskingPolicy{
				ID:       maskID(i, rng),
				Subject:  subjects[rng.Intn(len(subjects))],
				Attr:     attrs[rng.Intn(len(attrs))],
				Strength: rng.Intn(4),
				Rule:     randRule(rng, attrs),
			}
			if rng.Intn(2) == 0 {
				p.Subject = "*"
			}
			mask = append(mask, p)
		}

		eng := ontology.NewEngine(rng.Intn(2) == 0)
		ref := naive.New(true)
		// 参照实现使用相反的默认可见性时结果不可比，强制一致。
		eng = ontology.NewEngine(false)
		ref = naive.New(false)
		eng.SetSchema(schema)
		ref.SetSchema(schema)

		// 主引擎正序登记，参照实现逆序登记。
		for _, p := range vis {
			eng.RegisterVisibility(p)
		}
		for _, p := range mask {
			eng.RegisterMasking(p)
		}
		for i := len(vis) - 1; i >= 0; i-- {
			ref.RegisterVisibility(vis[i])
		}
		for i := len(mask) - 1; i >= 0; i-- {
			ref.RegisterMasking(mask[i])
		}

		subject := subjects[rng.Intn(len(subjects))]
		got := eng.Present(subject, inst)
		want := ref.Present(subject, inst)

		if !reflect.DeepEqual(got.Values, want.Values) {
			t.Fatalf("iter=%d Values 不一致\n got=%v\nwant=%v", iter, got.Values, want.Values)
		}
		if !reflect.DeepEqual(got.Outcome, want.Outcome) {
			t.Logf("subject=%v schema=%v inst=%v", subject, schema, inst)
			t.Logf("vis=%+v", vis)
			t.Logf("mask=%+v", mask)
			t.Fatalf("iter=%d Outcome 不一致\n got=%v\nwant=%v", iter, got.Outcome, want.Outcome)
		}
		if !reflect.DeepEqual(got.Errors, want.Errors) {
			t.Fatalf("iter=%d Errors 不一致\n got=%v\nwant=%v", iter, got.Errors, want.Errors)
		}
	}
}

func visID(i int, rng *rand.Rand) string {
	return fmt.Sprintf("v%02d", i)
}

func maskID(i int, rng *rand.Rand) string {
	return fmt.Sprintf("m%02d", i)
}

func randValue(rng *rand.Rand, t ontology.AttrType) ontology.Value {
	switch t.Kind {
	case ontology.KindString:
		n := rng.Intn(16)
		b := make([]byte, n)
		for i := range b {
			b[i] = byte('a' + rng.Intn(26))
		}
		return ontology.StringValue(string(b))
	case ontology.KindInt:
		return ontology.IntValue(int64(rng.Intn(120)))
	default:
		return ontology.BoolValue(rng.Intn(2) == 0)
	}
}

func randAnyValue(rng *rand.Rand) ontology.Value {
	switch rng.Intn(3) {
	case 0:
		return ontology.StringValue("x")
	case 1:
		return ontology.IntValue(int64(rng.Intn(120)))
	default:
		return ontology.BoolValue(true)
	}
}

func randRule(rng *rand.Rand, attrs []string) ontology.MaskingRule {
	switch rng.Intn(5) {
	case 0:
		return ontology.MaskingRule{Kind: ontology.RuleRedact}
	case 1:
		return ontology.MaskingRule{Kind: ontology.RuleHash}
	case 2:
		return ontology.MaskingRule{Kind: ontology.RuleTruncate, Param: rng.Intn(6)}
	case 3:
		return ontology.MaskingRule{Kind: ontology.RuleConstant, ParamValue: randAnyValue(rng)}
	default:
		return ontology.MaskingRule{Kind: ontology.RuleFromAttr, InputAttr: attrs[rng.Intn(len(attrs))]}
	}
}
