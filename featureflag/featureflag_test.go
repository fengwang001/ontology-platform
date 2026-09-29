package featureflag

import (
	"errors"
	"fmt"
	"testing"
)

func rw(weights ...[2]any) []RolloutWeight {
	out := make([]RolloutWeight, 0, len(weights))
	for _, w := range weights {
		out = append(out, RolloutWeight{Variant: w[0].(string), Weight: w[1].(int)})
	}
	return out
}

func mustPublish(t *testing.T, s *Store, spec SpecSet) int {
	t.Helper()
	v, err := s.Publish(spec)
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	return v
}

func errCode(err error) PublishErrorCode {
	var pe *PublishError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// 权重挪动：把后一个变体的权重挪给前一个变体后，
// 原本落在前一个变体的一万名用户必须全部保持不变。
func TestWeightShiftNoDrift(t *testing.T) {
	s := NewStore()

	base := FlagSpec{
		Enabled:        true,
		OffVariant:     "off",
		Variants:       []string{"off", "a", "b"},
		DefaultRollout: rw([2]any{"a", 2000}, [2]any{"b", 8000}),
	}
	mustPublish(t, s, SpecSet{"exp": base})

	beforeA := map[string]bool{}
	beforeB := map[string]bool{}
	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("user-%d", i)
		res, err := s.Evaluate("exp", EvalInput{UserID: uid})
		if err != nil {
			t.Fatal(err)
		}
		switch res.Variant {
		case "a":
			beforeA[uid] = true
		case "b":
			beforeB[uid] = true
		default:
			t.Fatalf("unexpected variant %q", res.Variant)
		}
	}
	// 哈希近似均匀：允许统计波动，但两个变体都必须有人命中。
	if len(beforeA) < 1500 || len(beforeA) > 2500 || len(beforeA)+len(beforeB) != 10000 {
		t.Fatalf("distribution a=%d b=%d, want roughly 2000/8000", len(beforeA), len(beforeB))
	}

	// 从后一个变体 b 挪 3000 给前一个变体 a。
	shifted := base
	shifted.DefaultRollout = rw([2]any{"a", 5000}, [2]any{"b", 5000})
	if v := mustPublish(t, s, SpecSet{"exp": shifted}); v != 2 {
		t.Fatalf("version after second publish = %d, want 2", v)
	}

	for i := 0; i < 10000; i++ {
		uid := fmt.Sprintf("user-%d", i)
		res, err := s.Evaluate("exp", EvalInput{UserID: uid})
		if err != nil {
			t.Fatal(err)
		}
		if beforeA[uid] && res.Variant != "a" {
			t.Fatalf("user %q drifted from a to %q after weight shift", uid, res.Variant)
		}
	}

	countA, countB := 0, 0
	for i := 0; i < 10000; i++ {
		res, _ := s.Evaluate("exp", EvalInput{UserID: fmt.Sprintf("user-%d", i)})
		if res.Variant == "a" {
			countA++
		} else {
			countB++
		}
	}
	// 挪动后 a 只能吸纳原来属于 b 的用户，且总量约为一半；
	// 关键不变式（原 a 用户不动）已在上面逐一验证。
	if countA <= len(beforeA) || countA < 4500 || countA > 5500 || countA+countB != 10000 {
		t.Fatalf("shifted distribution a=%d b=%d (before a=%d), want roughly 5000/5000 and growth",
			countA, countB, len(beforeA))
	}
}

// 三层前置链 a -> b -> c，中间一层 b 关闭时，a 返回关闭变体。
func TestThreeLayerPrerequisiteMiddleDisabled(t *testing.T) {
	s := NewStore()
	chain := func(bEnabled bool) SpecSet {
		return SpecSet{
			"c": {
				Enabled: true, OffVariant: "c_off", Variants: []string{"c_off", "c_on"},
				Rules: []Rule{{Name: "always", Variant: "c_on"}},
			},
			"b": {
				Enabled: bEnabled, OffVariant: "b_off", Variants: []string{"b_off", "b_on"},
				Prerequisites: []Prerequisite{{Flag: "c", Variant: "c_on"}},
				Rules:         []Rule{{Name: "always", Variant: "b_on"}},
			},
			"a": {
				Enabled: true, OffVariant: "a_off", Variants: []string{"a_off", "a_on"},
				Prerequisites: []Prerequisite{{Flag: "b", Variant: "b_on"}},
				Rules:         []Rule{{Name: "always", Variant: "a_on"}},
			},
		}
	}
	mustPublish(t, s, chain(true))

	res, err := s.Evaluate("a", EvalInput{UserID: "u1"})
	if err != nil || res.Variant != "a_on" || res.Reason != "rule:always:variant" {
		t.Fatalf("chain on: variant=%q reason=%q err=%v", res.Variant, res.Reason, err)
	}

	mustPublish(t, s, chain(false))
	res, err = s.Evaluate("a", EvalInput{UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Variant != "a_off" || res.Reason != "prerequisite_failed:b" {
		t.Fatalf("middle off: variant=%q reason=%q, want a_off/prerequisite_failed:b", res.Variant, res.Reason)
	}
}

// 缺少所引用属性视为规则不满足，落到默认放量。
func TestMissingAttributeSkipsRule(t *testing.T) {
	s := NewStore()
	mustPublish(t, s, SpecSet{
		"exp": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "vip", "base"},
			Rules: []Rule{{
				Name:       "vip-rule",
				Conditions: []Condition{{Attribute: "tier", Operator: OpEqual, Values: []any{"gold"}}},
				Variant:    "vip",
			}},
			DefaultRollout: rw([2]any{"base", 10000}),
		},
	})

	res, err := s.Evaluate("exp", EvalInput{UserID: "u1"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Variant != "base" || res.Reason != "default:rollout" {
		t.Fatalf("missing attr: variant=%q reason=%q", res.Variant, res.Reason)
	}

	res, err = s.Evaluate("exp", EvalInput{UserID: "u1", Attributes: map[string]any{"tier": "gold"}})
	if err != nil || res.Variant != "vip" || res.Reason != "rule:vip-rule:variant" {
		t.Fatalf("present attr: variant=%q reason=%q err=%v", res.Variant, res.Reason, err)
	}

	res, _ = s.Evaluate("exp", EvalInput{UserID: "u1", Attributes: map[string]any{"tier": "silver"}})
	if res.Variant != "base" {
		t.Fatalf("non-equal attr: variant=%q, want base", res.Variant)
	}
}

func TestDisabledFlagAndUnknownFlag(t *testing.T) {
	s := NewStore()
	mustPublish(t, s, SpecSet{
		"off-flag": {Enabled: false, OffVariant: "off", Variants: []string{"off", "on"}},
	})

	res, err := s.Evaluate("off-flag", EvalInput{UserID: "u"})
	if err != nil || res.Variant != "off" || res.Reason != "off" {
		t.Fatalf("disabled: %+v err=%v", res, err)
	}

	_, err = s.Evaluate("nope", EvalInput{UserID: "u"})
	var evalErr *EvalError
	if !errors.As(err, &evalErr) {
		t.Fatalf("unknown flag err = %v, want *EvalError", err)
	}
}
