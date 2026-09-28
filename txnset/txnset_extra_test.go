package txnset

import (
	"errors"
	"strings"
	"testing"
)

func TestStringMethod(t *testing.T) {
	s := mustParse(t, "b:2,a:1")
	if s.String() != s.Canonical() || s.String() != "a:1,b:2" {
		t.Fatalf("String() = %q", s.String())
	}
	if New().String() != "" {
		t.Fatalf("empty String() = %q", New().String())
	}
}

func TestEqualCases(t *testing.T) {
	base := mustParse(t, "a:1-3,b:5")

	if !base.Equal(mustParse(t, "b:5,a:1-3")) {
		t.Fatal("reordering should be equal")
	}
	if base.Equal(mustParse(t, "a:1-3")) {
		t.Fatal("missing source should differ")
	}
	if base.Equal(mustParse(t, "a:1-3,b:5,c:1")) {
		t.Fatal("extra source should differ")
	}
	if base.Equal(mustParse(t, "a:1-4,b:5")) {
		t.Fatal("different interval should differ")
	}
	if !New().Equal(New()) {
		t.Fatal("two empty sets should be equal")
	}
}

func TestMergeSelf(t *testing.T) {
	s := mustParse(t, "a:1-5")
	before := s.Canonical()
	s.Merge(s)
	if s.Canonical() != before {
		t.Fatalf("self-merge changed set: %q", s.Canonical())
	}
}

func TestNormalizeEmptyAndContainment(t *testing.T) {
	if got := normalizeIntervals(nil); got != nil {
		t.Fatalf("normalize(nil) = %v, want nil", got)
	}
	// 完全包含 + 同起点的乱序输入。
	got := normalizeIntervals([]Interval{{5, 5}, {1, 100}, {1, 3}, {50, 60}})
	want := []Interval{{1, 100}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestSubtractIntervalsFragmentation(t *testing.T) {
	// 多个切除点把一个区间切成碎片，验证 b 中区间不相邻时结果不合并。
	a := []Interval{{1, 20}}
	b := []Interval{{2, 2}, {4, 4}, {6, 8}, {20, 20}}
	got := subtractIntervals(a, b)
	want := []Interval{{1, 1}, {3, 3}, {5, 5}, {9, 19}}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("index %d: got %v, want %v", i, got[i], want[i])
		}
	}

	// 空 b 返回原区间内容（新切片）。
	got = subtractIntervals([]Interval{{1, 5}}, nil)
	if len(got) != 1 || got[0] != (Interval{1, 5}) {
		t.Fatalf("subtract nil = %v", got)
	}
}

func TestErrorKindString(t *testing.T) {
	if ErrorKind(99).String() != "unknown error" {
		t.Fatalf("unknown kind string = %q", ErrorKind(99).String())
	}
	if KindSyntax.String() != "syntax error" {
		t.Fatalf("syntax kind string = %q", KindSyntax.String())
	}
}

func TestParseErrorMessage(t *testing.T) {
	_, err := Parse("src:01")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("type = %T", err)
	}
	msg := pe.Error()
	if !strings.Contains(msg, "illegal transaction number") ||
		!strings.Contains(msg, "offset 4") {
		t.Fatalf("error message = %q", msg)
	}
}

func TestMergeSetOverlappingAcrossCalls(t *testing.T) {
	// 并发合并的串行化保证：同一切片反复合并，最终等价一次合并。
	s := New()
	for i := 0; i < 100; i++ {
		s.Merge(mustParse(t, "x:1-10"))
	}
	if got, want := s.Canonical(), "x:1-10"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestInt64Boundary(t *testing.T) {
	const max = "9223372036854775807"

	// MaxInt64-1 与 MaxInt64 相邻，必须合并为到 MaxInt64 的闭区间
	// （此前的实现会因 Hi+1 溢出而错误地保留两段）。
	s := mustParse(t, "s:9223372036854775806-"+max+",s:"+max)
	if got, want := s.Canonical(), "s:9223372036854775806-"+max; got != want {
		t.Fatalf("max-adjacent canonical = %q, want %q", got, want)
	}

	// 差集：[MaxInt64-5, MaxInt64] - [MaxInt64-2, MaxInt64]
	// = [MaxInt64-5, MaxInt64-3]，不得因 cut.Hi+1 溢出产出畸形区间。
	a := mustParse(t, "s:9223372036854775802-"+max)
	b := mustParse(t, "s:9223372036854775805-"+max)
	got := a.Diff(b).Canonical()
	want := "s:9223372036854775802-9223372036854775804"
	if got != want {
		t.Fatalf("boundary diff = %q, want %q", got, want)
	}

	// 减去单点 MaxInt64：结果上限为 MaxInt64-1。
	c := mustParse(t, "s:9223372036854775806-"+max)
	d := mustParse(t, "s:"+max)
	got = c.Diff(d).Canonical()
	want = "s:9223372036854775806"
	if got != want {
		t.Fatalf("point-max diff = %q, want %q", got, want)
	}
}
