package projection

import "testing"

func mustCompile(t *testing.T, cfg Config) *RuleSet {
	t.Helper()
	rs, err := Compile(cfg)
	if err != nil {
		t.Fatalf("Compile 失败: %v", err)
	}
	return rs
}

func TestParsePatternValid(t *testing.T) {
	for _, raw := range []string{"name", "addr.city", "addr.*", "*", "a-b_c.d9"} {
		if _, err := parsePattern(raw); err != nil {
			t.Errorf("parsePattern(%q) 应成功, 得到 %v", raw, err)
		}
	}
}

func TestParsePatternInvalid(t *testing.T) {
	cases := map[string]string{
		"":      "空模式",
		"a.*.b": "多层通配",
		"a..b":  "空段",
		".a":    "前导空段",
		"a.":    "尾部空段",
		"a*b":   "通配符未独占一段",
		"**":    "非法通配",
		"a.b c": "含空格",
		"a.b$c": "含非法字符",
	}
	for raw, why := range cases {
		if _, err := parsePattern(raw); err == nil {
			t.Errorf("parsePattern(%q) 应报错（%s）", raw, why)
		}
	}
}

func TestCompileReportsInvalidPatternLocation(t *testing.T) {
	_, err := Compile(Config{Deny: []string{"ok", "a.*.b"}})
	pe, ok := err.(*PatternError)
	if !ok {
		t.Fatalf("应返回 *PatternError, 得到 %T: %v", err, err)
	}
	if pe.Set != "deny" || pe.Index != 1 || pe.Pattern != "a.*.b" {
		t.Errorf("错误定位不正确: %+v", pe)
	}
}

func TestCompileConflictAllowAndDeny(t *testing.T) {
	_, err := Compile(Config{
		Allow: []string{"addr.city"},
		Deny:  []string{"addr.city"},
	})
	ce, ok := err.(*ConflictError)
	if !ok {
		t.Fatalf("完全相同的模式同时允许与拒绝应报 *ConflictError, 得到 %T: %v", err, err)
	}
	if ce.Pattern != "addr.city" {
		t.Errorf("冲突模式应为 addr.city, 得到 %q", ce.Pattern)
	}
}

func TestCompileDuplicateInSameSetIsDeduplicated(t *testing.T) {
	rs := mustCompile(t, Config{Deny: []string{"ssn", "ssn"}})
	if got := len(rs.rules); got != 1 {
		t.Errorf("同集合重复模式应去重, 规则数 = %d", got)
	}
}

func TestWildcardMatchesSingleLevelOnly(t *testing.T) {
	p, err := parsePattern("addr.*")
	if err != nil {
		t.Fatal(err)
	}
	if !p.matches([]string{"addr", "city"}) {
		t.Error("addr.* 应匹配 addr.city")
	}
	if p.matches([]string{"addr"}) {
		t.Error("addr.* 不应匹配 addr 本身")
	}
	if p.matches([]string{"addr", "geo", "lat"}) {
		t.Error("addr.* 不应跨层匹配 addr.geo.lat")
	}
}

func TestSpecificityOrdersExactOverWildcard(t *testing.T) {
	exact, _ := parsePattern("addr.geo")
	wild, _ := parsePattern("addr.*")
	if exact.specificity() <= wild.specificity() {
		t.Error("addr.geo 的具体度应高于 addr.*")
	}
}
