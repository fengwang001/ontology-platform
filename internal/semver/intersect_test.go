package semver

import (
	"errors"
	"strings"
	"testing"
)

func TestIntersectBasic(t *testing.T) {
	cases := []struct {
		name      string
		ranges    []string
		wantMatch map[string]bool
	}{
		{
			"overlap",
			[]string{">=1.0.0 <2.0.0", ">=1.5.0 <3.0.0"},
			map[string]bool{"1.4.9": false, "1.5.0": true, "1.9.9": true, "2.0.0": false},
		},
		{
			"single point",
			[]string{">=1.2.3", "<=1.2.3"},
			map[string]bool{"1.2.2": false, "1.2.3": true, "1.2.4": false},
		},
		{
			"inclusive boundary not conflict",
			[]string{"<=1.2.3", ">=1.2.3"},
			map[string]bool{"1.2.3": true},
		},
		{
			"caret overlap",
			[]string{"^1.2.3", "~1.2.5"},
			map[string]bool{"1.2.4": false, "1.2.5": true, "1.2.9": true, "1.3.0": false},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rs := make([]Range, len(c.ranges))
			for i, s := range c.ranges {
				rs[i] = mustRange(t, s)
			}
			inter, err := Intersect(rs...)
			if err != nil {
				t.Fatalf("Intersect: %v", err)
			}
			for ver, want := range c.wantMatch {
				if got := inter.Match(mustParse(t, ver)); got != want {
					t.Errorf("intersection.Match(%q) = %v, want %v", ver, got, want)
				}
			}
		})
	}
}

func TestIntersectConflicts(t *testing.T) {
	cases := []struct {
		ranges  []string
		wantRaw []string // 错误信息里必须出现的两条原文
	}{
		{[]string{"<1.2.3", ">=1.2.3"}, []string{"<1.2.3", ">=1.2.3"}},
		{[]string{">2.0.0", "<=1.0.0"}, []string{">2.0.0", "<=1.0.0"}},
		{[]string{">=1.0.0 <2.0.0", ">=3.0.0"}, []string{">=3.0.0", "<2.0.0"}},
		{[]string{"^0.0.3", ">=0.0.4"}, []string{"^0.0.3", ">=0.0.4"}},
		// >=1.0.0 未对 2.0.0 三元组放行预发布，与 =2.0.0-rc.1 直接冲突；
		// <2.0.0-0 也同样拒绝 rc.1，二者任选其一作为冲突解释均成立。
		{[]string{">=1.0.0 <2.0.0-0", "=2.0.0-rc.1"}, []string{"=2.0.0-rc.1"}},
		{[]string{"=1.0.0-alpha", "=1.0.0-beta"}, []string{"=1.0.0-alpha", "=1.0.0-beta"}},
	}
	for _, c := range cases {
		rs := make([]Range, len(c.ranges))
		for i, s := range c.ranges {
			rs[i] = mustRange(t, s)
		}
		_, err := Intersect(rs...)
		if err == nil {
			t.Fatalf("Intersect(%v) = nil error, want conflict", c.ranges)
		}
		if !errors.Is(err, ErrEmptyIntersection) {
			t.Fatalf("error %v does not wrap ErrEmptyIntersection", err)
		}
		var ce *ConflictError
		if !errors.As(err, &ce) {
			t.Fatalf("error %v is not *ConflictError", err)
		}
		msg := err.Error()
		for _, raw := range c.wantRaw {
			if !strings.Contains(msg, raw) {
				t.Errorf("conflict message %q missing constraint %q", msg, raw)
			}
		}
	}
}

func TestIntersectPrereleasePoint(t *testing.T) {
	// 交集只剩一个预发布点，且两个原始 range 都放行该三元组。
	r1 := mustRange(t, ">=1.0.0-alpha <1.0.0-beta")
	r2 := mustRange(t, ">=1.0.0-alpha.2 <=1.0.0-alpha.2")
	inter, err := Intersect(r1, r2)
	if err != nil {
		t.Fatalf("Intersect: %v", err)
	}
	if !inter.Match(mustParse(t, "1.0.0-alpha.2")) {
		t.Error("intersection must match 1.0.0-alpha.2")
	}
	if inter.Match(mustParse(t, "1.0.0-alpha.3")) {
		t.Error("intersection must not match 1.0.0-alpha.3")
	}
	if inter.Match(mustParse(t, "1.0.0")) {
		t.Error("intersection must not match release 1.0.0")
	}
}

func TestIntersectGateRejected(t *testing.T) {
	// release 区间重叠，但预发布点只被其中一个 range 放行 → 不冲突，
	// 但任何预发布版本都无法同时匹配两个 range。
	r1 := mustRange(t, ">=1.0.0-0 <1.1.0")
	r2 := mustRange(t, ">=1.0.0")
	inter, err := Intersect(r1, r2)
	if err != nil {
		t.Fatalf("release 1.0.5 still satisfies both: %v", err)
	}
	if inter.Match(mustParse(t, "1.0.5-alpha")) {
		t.Error("1.0.5-alpha is not gated by the second range")
	}
	if !inter.Match(mustParse(t, "1.0.5")) {
		t.Error("release 1.0.5 must match")
	}
}
