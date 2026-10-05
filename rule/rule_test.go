package rule_test

import (
	"testing"

	"ontology/rule"
)

func TestParse(t *testing.T) {
	valid := map[string]rule.When{
		"on_success": rule.OnSuccess,
		"on_failure": rule.OnFailure,
		"always":     rule.Always,
		"manual":     rule.Manual,
	}
	for s, want := range valid {
		got, ok := rule.Parse(s)
		if !ok || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v, true", s, got, ok, want)
		}
	}
	for _, s := range []string{"", "ON_SUCCESS", "sometimes", "never"} {
		if _, ok := rule.Parse(s); ok {
			t.Errorf("Parse(%q) accepted invalid when", s)
		}
	}
}

func TestEvaluate(t *testing.T) {
	cases := []struct {
		when      rule.When
		bad, skip bool
		want      rule.Decision
	}{
		{rule.OnSuccess, false, false, rule.ToPending},
		{rule.OnSuccess, true, false, rule.ToSkipped},
		{rule.OnSuccess, false, true, rule.ToSkipped},
		{rule.OnSuccess, true, true, rule.ToSkipped},
		{rule.OnFailure, false, false, rule.ToSkipped}, // 无失败即跳过，含无 needs
		{rule.OnFailure, true, false, rule.ToPending},
		{rule.OnFailure, false, true, rule.ToSkipped}, // 上游被跳过不算失败
		{rule.OnFailure, true, true, rule.ToPending},
		{rule.Always, false, false, rule.ToPending},
		{rule.Always, true, false, rule.ToPending},
		{rule.Always, false, true, rule.ToPending},
		{rule.Always, true, true, rule.ToPending},
		{rule.Manual, false, false, rule.ToManual},
		{rule.Manual, true, false, rule.ToSkipped},
		{rule.Manual, false, true, rule.ToSkipped},
		{rule.Manual, true, true, rule.ToSkipped},
	}
	for _, c := range cases {
		got := rule.Evaluate(c.when, c.bad, c.skip)
		if got != c.want {
			t.Errorf("Evaluate(%v, bad=%v, skip=%v) = %v; want %v", c.when, c.bad, c.skip, got, c.want)
		}
	}
}
