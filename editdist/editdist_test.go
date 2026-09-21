package editdist

import (
	"errors"
	"strings"
	"testing"
)

// naiveDist 是测试用的参照实现：完整 DP 表，无阈值、无带状裁剪。
func naiveDist(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	n, m := len(ra), len(rb)
	d := make([][]int, n+1)
	for i := range d {
		d[i] = make([]int, m+1)
		d[i][0] = i
	}
	for j := 0; j <= m; j++ {
		d[0][j] = j
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+c)
		}
	}
	return d[n][m]
}

func TestExactDistanceMatchesFull(t *testing.T) {
	cases := []struct{ a, b string }{
		{"kitten", "sitting"},
		{"flaw", "lawn"},
		{"abc", "abc"},
		{"", ""},
		{"gumbo", "gambol"},
		{"café", "cafe"},
		{"a🙂b", "ab"},
	}
	for _, tc := range cases {
		full, err := Full(tc.a, tc.b, Options{})
		if err != nil {
			t.Fatalf("Full(%q,%q): %v", tc.a, tc.b, err)
		}
		if want := naiveDist(tc.a, tc.b); full.Distance != want {
			t.Fatalf("Full(%q,%q)=%d, naive=%d", tc.a, tc.b, full.Distance, want)
		}
		r, err := Distance(tc.a, tc.b, full.Distance)
		if err != nil {
			t.Fatalf("Distance(%q,%q): %v", tc.a, tc.b, err)
		}
		if r.Exceeded || r.Distance != full.Distance {
			t.Errorf("Distance(%q,%q,k=%d)=%d exceeded=%v, want exact %d",
				tc.a, tc.b, full.Distance, r.Distance, r.Exceeded, full.Distance)
		}
		if full.Distance > 0 {
			under, _ := Distance(tc.a, tc.b, full.Distance-1)
			if !under.Exceeded {
				t.Errorf("Distance(%q,%q,k=%d) should exceed", tc.a, tc.b, full.Distance-1)
			}
		}
	}
}

func TestNegativeK(t *testing.T) {
	_, err := Distance("a", "b", -1)
	if !errors.Is(err, ErrNegativeK) {
		t.Fatalf("want ErrNegativeK, got %v", err)
	}
}

func TestKZeroIsEquality(t *testing.T) {
	eq, err := Distance("same", "same", 0)
	if err != nil || eq.Exceeded || eq.Distance != 0 {
		t.Fatalf("equal strings k=0: %+v err=%v", eq, err)
	}
	neq, err := Distance("same", "diff", 0)
	if err != nil || !neq.Exceeded {
		t.Fatalf("unequal strings k=0: %+v err=%v", neq, err)
	}
}

func TestEmptyStrings(t *testing.T) {
	both, _ := Distance("", "", 0)
	if both.Exceeded || both.Distance != 0 {
		t.Fatalf("both empty: %+v", both)
	}
	one, _ := Distance("", "abc", 3)
	if one.Exceeded || one.Distance != 3 {
		t.Fatalf("one empty within k: %+v", one)
	}
	over, _ := Distance("", "abc", 2)
	if !over.Exceeded {
		t.Fatalf("one empty beyond k: %+v", over)
	}
	rev, _ := Distance("abc", "", 3)
	if rev.Exceeded || rev.Distance != 3 {
		t.Fatalf("one empty reversed: %+v", rev)
	}
}

func TestLengthDiffSkipsAllCells(t *testing.T) {
	r, err := Distance("abc", "abcdefgh", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Exceeded {
		t.Fatal("length diff 5 > k=2 must exceed")
	}
	if r.Stats.CellsFilled != 0 {
		t.Fatalf("CellsFilled=%d, want 0", r.Stats.CellsFilled)
	}
}

func TestSymmetry(t *testing.T) {
	pairs := []struct{ a, b string }{
		{"kitten", "sitting"},
		{"abcdef", "azced"},
		{"", "xyz"},
		{strings.Repeat("ab", 50), strings.Repeat("ba", 40)},
	}
	for _, k := range []int{0, 1, 3, 10} {
		for _, p := range pairs {
			fwd, _ := Distance(p.a, p.b, k)
			rev, _ := Distance(p.b, p.a, k)
			if fwd.Exceeded != rev.Exceeded || fwd.Distance != rev.Distance {
				t.Errorf("asymmetric result k=%d %q/%q: %+v vs %+v", k, p.a, p.b, fwd, rev)
			}
			if fwd.Stats.CellsFilled != rev.Stats.CellsFilled {
				t.Errorf("asymmetric cells k=%d %q/%q: %d vs %d",
					k, p.a, p.b, fwd.Stats.CellsFilled, rev.Stats.CellsFilled)
			}
		}
	}
}

func TestCaseFold(t *testing.T) {
	plain, _ := Distance("Hello", "hELLO", 5)
	if plain.Exceeded || plain.Distance == 0 {
		t.Fatalf("without fold should differ: %+v", plain)
	}
	folded, err := Within("Hello", "hELLO", 0, Options{CaseFold: true})
	if err != nil || folded.Exceeded || folded.Distance != 0 {
		t.Fatalf("with fold should be equal: %+v err=%v", folded, err)
	}
	// 简单折叠覆盖单码点等价类 ß/ẞ。
	sharp, _ := Within("ß", "ẞ", 0, Options{CaseFold: true})
	if sharp.Exceeded || sharp.Distance != 0 {
		t.Fatalf("ß/ẞ should fold-equal: %+v", sharp)
	}
	// 全折叠（ß→ss）不在承诺范围内：Straße 与 STRASSE 不匹配。
	full, _ := Within("Straße", "STRASSE", 0, Options{CaseFold: true})
	if !full.Exceeded {
		t.Fatalf("full folding must NOT be applied: %+v", full)
	}
}
