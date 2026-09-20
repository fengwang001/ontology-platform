package semver

import "testing"

func TestPrereleaseGating(t *testing.T) {
	cases := []struct {
		expr string
		v    string
		want bool
	}{
		// 数值上更大，但没有同三元组的预发布操作数 => 不匹配。
		{">=1.0.0", "1.1.0-alpha", false},
		{">1.0.0", "2.0.0-alpha", false},
		{"^1.0.0", "1.5.0-beta", false},
		{"~1.2.0", "1.2.5-rc", false},
		// 显式同三元组预发布操作数 => 可以匹配。
		{">=1.1.0-0", "1.1.0-alpha", true},
		{"=1.2.3-alpha", "1.2.3-alpha", true},
		{"=1.2.3-alpha", "1.2.3-beta", false},
		// ^/~ 展开后的上界 < 也必须满足。
		{"^1.2.0", "2.0.0-alpha", false},
		// 正式版不受门控影响。
		{">=1.0.0", "1.0.0", true},
		{">=1.0.0", "1.1.0", true},
		// 多条约束里只要有一条给出同三元组预发布许可即可，且其余约束仍须满足。
		{">=1.0.0 <2.0.0", "1.5.0-alpha", false},
		{">=1.5.0-0 <2.0.0", "1.5.0-alpha", true},
		{">=1.5.0-0 <2.0.0", "1.5.0-alpha.5", true},
		{">=1.5.0-0 <2.0.0", "1.6.0-alpha", false},
	}
	for _, c := range cases {
		r := parseRange(t, c.expr)
		if got := r.Match(mustParse(t, c.v)); got != c.want {
			t.Errorf("%q.Match(%q) = %v, want %v", c.expr, c.v, got, c.want)
		}
	}
}

func TestPrereleaseGateSameTupleOnly(t *testing.T) {
	// 许可只针对与操作数同三元组的预发布版本。
	r := parseRange(t, ">=1.1.0-0")
	if r.Match(mustParse(t, "1.2.0-alpha")) {
		t.Error("1.2.0-alpha must not be allowed by >=1.1.0-0")
	}
	if !r.Match(mustParse(t, "1.1.0-alpha")) {
		t.Error("1.1.0-alpha must be allowed by >=1.1.0-0")
	}
}
