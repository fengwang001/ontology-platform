package ontology

import "testing"

func TestParseValidPatterns(t *testing.T) {
	valid := []string{"a", "addr.*", "a_b-c.d", "*", "a.b.c"}
	for _, raw := range valid {
		if _, err := parsePattern(raw); err != nil {
			t.Errorf("parsePattern(%q) unexpected error: %v", raw, err)
		}
	}
}

func TestParseInvalidPatterns(t *testing.T) {
	invalid := []string{
		"",      // 空模式
		"a..b",  // 空段
		".a",    // 前导点
		"a.",    // 尾随点
		"a.*.b", // 多层通配
		"*.*",   // 非末段通配
		"ab*",   // 段内嵌通配
		"a.*b",  // 段内嵌通配
		"a b",   // 非法字符
		"a.中文",  // 非法字符
	}
	for _, raw := range invalid {
		if _, err := parsePattern(raw); err == nil {
			t.Errorf("parsePattern(%q) expected error, got nil", raw)
		}
	}
}

func TestWildcardMatchesSingleLevelOnly(t *testing.T) {
	p, err := parsePattern("addr.*")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cases := []struct {
		path []string
		want bool
	}{
		{[]string{"addr", "city"}, true},
		{[]string{"addr", "geo"}, true},
		{[]string{"addr"}, false},               // 层数不同
		{[]string{"addr", "geo", "lat"}, false}, // 不跨层
		{[]string{"other", "city"}, false},      // 前缀不同
	}
	for _, c := range cases {
		if got := p.matches(c.path); got != c.want {
			t.Errorf("addr.* matches %v = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestSpecificityOrdering(t *testing.T) {
	exact, _ := parsePattern("addr.city")
	wild, _ := parsePattern("addr.*")
	deep, _ := parsePattern("addr.geo.lat")
	if !moreSpecific(exact, wild) {
		t.Error("addr.city should be more specific than addr.*")
	}
	if moreSpecific(wild, exact) {
		t.Error("addr.* should not be more specific than addr.city")
	}
	if !moreSpecific(deep, exact) {
		t.Error("addr.geo.lat should be more specific than addr.city")
	}
	if !sameSpecificity(wild, wild) {
		t.Error("identical patterns should have same specificity")
	}
}
