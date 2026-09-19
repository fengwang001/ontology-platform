package ontology

import (
	"errors"
	"testing"
)

func mustCompile(t *testing.T, allow, deny []string) *RuleSet {
	t.Helper()
	rs, err := Compile(allow, deny)
	if err != nil {
		t.Fatalf("Compile(%v, %v): %v", allow, deny, err)
	}
	return rs
}

func TestCompileIdenticalAllowDenyIsConflict(t *testing.T) {
	_, err := Compile([]string{"addr.city"}, []string{"addr.city"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected ConflictError, got %v", err)
	}
	if conflict.Pattern != "addr.city" {
		t.Errorf("conflict pattern = %q", conflict.Pattern)
	}
}

func TestCompileInvalidPatternReportsIndexAndRaw(t *testing.T) {
	_, err := Compile([]string{"ok", "a..b"}, nil)
	var perr *PatternError
	if !errors.As(err, &perr) {
		t.Fatalf("expected PatternError, got %v", err)
	}
	if perr.Set != "allow" || perr.Index != 1 || perr.Raw != "a..b" {
		t.Errorf("unexpected PatternError: %+v", perr)
	}
	_, err = Compile(nil, []string{"good", "also.good", "a.*.b"})
	if !errors.As(err, &perr) {
		t.Fatalf("expected PatternError, got %v", err)
	}
	if perr.Set != "deny" || perr.Index != 2 || perr.Raw != "a.*.b" {
		t.Errorf("unexpected PatternError: %+v", perr)
	}
}

func TestMoreSpecificRuleWins(t *testing.T) {
	rs := mustCompile(t, []string{"addr.*"}, []string{"addr.city"})
	exp := rs.Explain("addr.city")
	if exp.Visible || exp.Rule != "addr.city" || exp.Kind != "deny" {
		t.Errorf("addr.city: %+v", exp)
	}
	exp = rs.Explain("addr.zip")
	if !exp.Visible || exp.Rule != "addr.*" || exp.Kind != "allow" {
		t.Errorf("addr.zip: %+v", exp)
	}
	// 反向：更具体的允许胜过更泛的拒绝。
	rs = mustCompile(t, []string{"addr.city"}, []string{"addr.*"})
	if !rs.Visible("addr.city") {
		t.Error("addr.city should be visible via more specific allow")
	}
	if rs.Visible("addr.zip") {
		t.Error("addr.zip should be hidden by addr.*")
	}
}

func TestEqualSpecificityDenyWins(t *testing.T) {
	// 相同具体度的允许与拒绝（绕过 Compile 的冲突检查构造）：
	// 拒绝必须获胜，保证确定性。
	wild, _ := parsePattern("addr.*")
	rs := &RuleSet{allows: []pattern{wild}, denies: []pattern{wild}}
	exp := rs.Explain("addr.city")
	if exp.Visible || exp.Kind != "deny" || exp.Reason != ReasonDenyRule {
		t.Errorf("equal specificity should deny, got %+v", exp)
	}
}

func TestDefaultVisibility(t *testing.T) {
	withAllow := mustCompile(t, []string{"id"}, nil)
	exp := withAllow.Explain("name")
	if exp.Visible || exp.Reason != ReasonDefaultDeny || exp.Rule != "" {
		t.Errorf("default deny: %+v", exp)
	}
	noAllow := mustCompile(t, nil, []string{"secret"})
	exp = noAllow.Explain("name")
	if !exp.Visible || exp.Reason != ReasonDefaultAllow {
		t.Errorf("default allow: %+v", exp)
	}
}

func TestAncestorDenyOverridesExplicitAllow(t *testing.T) {
	rs := mustCompile(t, []string{"addr.geo.lat"}, []string{"addr"})
	exp := rs.Explain("addr.geo.lat")
	if exp.Visible {
		t.Fatal("descendant of denied parent must be hidden")
	}
	if exp.Reason != ReasonAncestorDeny {
		t.Errorf("reason = %v, want ReasonAncestorDeny", exp.Reason)
	}
	if exp.Rule != "addr" {
		t.Errorf("rule = %q, want the ancestor rule %q", exp.Rule, "addr")
	}
}

func TestAncestorDenyByWildcardCoversDeeper(t *testing.T) {
	// addr.* 拒绝 addr.geo（直接子字段），addr.geo.lat 因此被祖先规则覆盖。
	rs := mustCompile(t, nil, []string{"addr.*"})
	exp := rs.Explain("addr.geo.lat")
	if exp.Visible || exp.Reason != ReasonAncestorDeny || exp.Rule != "addr.*" {
		t.Errorf("addr.geo.lat: %+v", exp)
	}
}
