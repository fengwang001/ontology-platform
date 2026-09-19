package projection

import "testing"

func TestExplainNoRuleDefaultVisible(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"ssn"}})
	exp := rs.Explain("name")
	if !exp.Visible || exp.Effect != Default || exp.Rule != "" {
		t.Errorf("无规则命中应默认可见且无命中规则: %+v", exp)
	}
}

func TestExplainReturnsMatchedRuleText(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"salary"}})
	exp := rs.Explain("salary")
	if exp.Visible || exp.Effect != Deny || exp.Rule != "salary" {
		t.Errorf("应命中拒绝规则原文 salary: %+v", exp)
	}
}

func TestMoreSpecificRuleWins(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:  []string{"addr.*"},
		Allow: []string{"addr.city"},
	})
	exp := rs.Explain("addr.city")
	if !exp.Visible || exp.Rule != "addr.city" {
		t.Errorf("更具体的允许规则应胜出: %+v", exp)
	}
	exp = rs.Explain("addr.street")
	if exp.Visible || exp.Rule != "addr.*" {
		t.Errorf("addr.street 应被 addr.* 拒绝: %+v", exp)
	}
}

func TestDeeperExactBeatsShallowerWildcard(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:  []string{"addr.*"},
		Allow: []string{"addr.geo"},
	})
	if exp := rs.Explain("addr.geo"); !exp.Visible {
		t.Errorf("addr.geo 应被更具体的允许规则保留: %+v", exp)
	}
	if exp := rs.Explain("addr.geo.lat"); !exp.Visible || exp.Effect != Default {
		t.Errorf("addr.geo.lat 不应被 addr.* 波及: %+v", exp)
	}
}

func TestAncestorDenyOverridesExplicitAllow(t *testing.T) {
	rs := mustCompile(t, Config{
		Deny:  []string{"addr"},
		Allow: []string{"addr.city"},
	})
	exp := rs.Explain("addr.city")
	if exp.Visible {
		t.Error("父字段被拒时后代应一律不可见")
	}
	if !exp.Covered || exp.AncestorPath != "addr" || exp.Rule != "addr" {
		t.Errorf("应解释为被祖先规则 addr 覆盖: %+v", exp)
	}
	exp = rs.Explain("addr.geo.lat")
	if exp.Visible || !exp.Covered || exp.Rule != "addr" {
		t.Errorf("更深层后代同样被祖先规则覆盖: %+v", exp)
	}
}

func TestWildcardDeniedAncestorCoversDeeperLevels(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"addr.*"}})
	exp := rs.Explain("addr.geo.lat")
	if exp.Visible || !exp.Covered || exp.AncestorPath != "addr.geo" {
		t.Errorf("addr.geo 被拒后 addr.geo.lat 应被覆盖: %+v", exp)
	}
}
