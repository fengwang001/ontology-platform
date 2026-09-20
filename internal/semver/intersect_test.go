package semver

import (
	"errors"
	"strings"
	"testing"
)

func TestIntersectPoint(t *testing.T) {
	r, err := Intersect(parseRange(t, ">=1.2.3"), parseRange(t, "<=1.2.3"))
	if err != nil {
		t.Fatalf("point intersection must be legal: %v", err)
	}
	if !r.Match(mustParse(t, "1.2.3")) {
		t.Error("1.2.3 must match the point intersection")
	}
	if r.Match(mustParse(t, "1.2.3-alpha")) {
		t.Error("1.2.3-alpha must not match point intersection without prerelease operand")
	}
	if r.Match(mustParse(t, "1.2.4")) {
		t.Error("1.2.4 must not match point intersection")
	}
}

func TestIntersectConflict(t *testing.T) {
	cases := []struct {
		name      string
		left      string
		right     string
		wantLeft  string
		wantRight string
	}{
		{"strict boundary", "<1.2.3", ">=1.2.3", "<1.2.3", ">=1.2.3"},
		{"caret vs higher", "^1.2.3", ">=2.0.0", "^1.2.3", ">=2.0.0"},
		{"caret 0.0.x", "^0.0.3", ">=0.0.4", "^0.0.3", ">=0.0.4"},
		{"caret 0.x", "^0.2.3", ">=0.3.0", "^0.2.3", ">=0.3.0"},
		{"both strict", ">1.2.3", "<1.2.3", ">1.2.3", "<1.2.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Intersect(parseRange(t, c.left), parseRange(t, c.right))
			if err == nil {
				t.Fatal("expected conflict error")
			}
			var ce *ConflictError
			if !errors.As(err, &ce) {
				t.Fatalf("err = %T %v, want *ConflictError", err, err)
			}
			if ce.Left != c.wantLeft || ce.Right != c.wantRight {
				t.Errorf("conflict raws = %q, %q, want %q, %q",
					ce.Left, ce.Right, c.wantLeft, c.wantRight)
			}
			if !strings.Contains(err.Error(), c.wantLeft) ||
				!strings.Contains(err.Error(), c.wantRight) {
				t.Errorf("error %q must mention both raw constraints", err)
			}
		})
	}
}

func TestIntersectCompatible(t *testing.T) {
	r, err := Intersect(parseRange(t, "^1.2.3"), parseRange(t, ">=1.5.0"))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Match(mustParse(t, "1.5.0")) || !r.Match(mustParse(t, "1.9.9")) {
		t.Error("1.5.0/1.9.9 should match")
	}
	for _, bad := range []string{"1.4.9", "2.0.0"} {
		if r.Match(mustParse(t, bad)) {
			t.Errorf("%s should not match", bad)
		}
	}
}

// TestIntersectMatchEquivalence 是硬要求：求交结果的 Match 必须与
// “对每个输入 Range 分别 Match 再取与”完全等价。
func TestIntersectMatchEquivalence(t *testing.T) {
	exprs := []string{
		">=1.0.0", "<2.0.0", "^1.2.0", "~1.2.3", ">1.1.0", "<=1.5.0",
		"=1.2.3", ">=1.2.0", "<1.4.0", ">=1.3.0-0", "^0.2.0", ">=2.0.0",
	}
	versions := []string{
		"0.2.0", "0.2.5", "0.3.0",
		"1.0.0", "1.1.0", "1.1.9",
		"1.2.0", "1.2.3", "1.2.9", "1.3.0", "1.3.5", "1.4.0", "1.5.0", "1.9.9",
		"2.0.0", "2.1.0",
		"1.2.3-alpha", "1.3.0-alpha", "1.3.0-beta.2", "2.0.0-rc.1",
	}
	ranges := make([]Range, len(exprs))
	for i, e := range exprs {
		ranges[i] = parseRange(t, e)
	}

	// 枚举所有两两与三三组合。
	combos := [][]int{}
	for i := range ranges {
		for j := i + 1; j < len(ranges); j++ {
			combos = append(combos, []int{i, j})
			for k := j + 1; k < len(ranges); k++ {
				combos = append(combos, []int{i, j, k})
			}
		}
	}
	for _, idx := range combos {
		inputs := make([]Range, len(idx))
		for n, ix := range idx {
			inputs[n] = ranges[ix]
		}
		inter, err := Intersect(inputs...)
		for _, vs := range versions {
			v := mustParse(t, vs)
			want := true
			for _, in := range inputs {
				want = want && in.Match(v)
			}
			if err != nil {
				if want {
					t.Errorf("exprs %v: reported conflict but %s matches all ranges",
						exprsOf(exprs, idx), vs)
				}
				continue
			}
			if got := inter.Match(v); got != want {
				t.Errorf("Intersect(%v).Match(%s) = %v, want AND of each Match = %v",
					exprsOf(exprs, idx), vs, got, want)
			}
		}
	}
}

func exprsOf(exprs []string, idx []int) []string {
	out := make([]string, len(idx))
	for i, ix := range idx {
		out[i] = exprs[ix]
	}
	return out
}

func TestIntersectNoArgs(t *testing.T) {
	r, err := Intersect()
	if err != nil {
		t.Fatal(err)
	}
	if !r.Match(mustParse(t, "9.9.9")) {
		t.Error("empty intersection should match everything")
	}
}

func TestIntersectPrereleaseGateNotBorrowed(t *testing.T) {
	// 一个输入范围给出 1.2.0 元组的预发布许可，另一个只认 1.3.0 元组；
	// 门控不得跨输入范围借用，1.3.0-alpha 必须不能被求交结果匹配。
	r, err := Intersect(parseRange(t, ">=1.2.0-0"), parseRange(t, ">=1.3.0 <2.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Match(mustParse(t, "1.3.0-alpha")) {
		t.Error("prerelease permission must not be borrowed across input ranges")
	}
	if !r.Match(mustParse(t, "1.3.0")) {
		t.Error("1.3.0 release should match")
	}
}
