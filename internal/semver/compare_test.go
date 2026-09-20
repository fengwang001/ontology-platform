package semver

import "testing"

func mustParse(t *testing.T, s string) Version {
	t.Helper()
	v, err := Parse(s)
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return v
}

func TestCompareCore(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"1.2.3", "1.2.3", 0},
	}
	for _, c := range cases {
		if got := Compare(mustParse(t, c.a), mustParse(t, c.b)); got != c.want {
			t.Errorf("Compare(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestComparePrecedence(t *testing.T) {
	// SemVer 2.0.0 规范第 11 条示例优先级链。
	chain := []string{
		"1.0.0-alpha",
		"1.0.0-alpha.1",
		"1.0.0-alpha.beta",
		"1.0.0-beta",
		"1.0.0-beta.2",
		"1.0.0-beta.11",
		"1.0.0-rc.1",
		"1.0.0",
	}
	for i := 0; i < len(chain); i++ {
		for j := 0; j < len(chain); j++ {
			got := Compare(mustParse(t, chain[i]), mustParse(t, chain[j]))
			want := 0
			if i < j {
				want = -1
			} else if i > j {
				want = 1
			}
			if got != want {
				t.Errorf("Compare(%q,%q) = %d, want %d", chain[i], chain[j], got, want)
			}
		}
	}
}

func TestCompareNumericIdent(t *testing.T) {
	// 纯数字标识符按数值比较：10 > 9。
	if Compare(mustParse(t, "1.0.0-10"), mustParse(t, "1.0.0-9")) != 1 {
		t.Fatal("numeric prerelease id 10 must be greater than 9")
	}
	// 数字段永远低于含字母的段。
	if Compare(mustParse(t, "1.0.0-1"), mustParse(t, "1.0.0-alpha")) != -1 {
		t.Fatal("numeric id must rank below alphanumeric id")
	}
	// 段数多的更大。
	if Compare(mustParse(t, "1.0.0-a.1"), mustParse(t, "1.0.0-a")) != 1 {
		t.Fatal("longer prerelease with equal prefix must be greater")
	}
	// 带预发布低于同三元组的正式版。
	if Compare(mustParse(t, "1.0.0-alpha"), mustParse(t, "1.0.0")) != -1 {
		t.Fatal("prerelease must rank below release")
	}
}

func TestCompareIgnoresBuild(t *testing.T) {
	if !Equal(mustParse(t, "1.0.0+x"), mustParse(t, "1.0.0+y")) {
		t.Fatal("build metadata must not affect precedence")
	}
	if Compare(mustParse(t, "1.0.0-alpha+x"), mustParse(t, "1.0.0-alpha+y")) != 0 {
		t.Fatal("build metadata must not affect precedence (prerelease)")
	}
}
