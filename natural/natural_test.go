package natural

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want int
	}{
		{"basic-file", "file2", "file10", -1},
		{"basic-mixed", "a9b", "a10a", -1},
		{"basic-prefix", "x", "x1", -1},
		{"bignum-40-digit", "f" + strings.Repeat("9", 40), "f1" + strings.Repeat("0", 40), -1},
		{"zeros-short-first", "a1", "a01", -1},
		{"zeros-chain", "a01", "a001", -1},
		{"zeros-defer-to-tail", "a01b", "a1c", -1},
		{"zeros-first-diff-wins", "a1b01", "a01b1", -1},
		{"zeros-x0", "x0", "x00", -1},
		{"zeros-x00", "x00", "x000", -1},
		{"zeros-before-one", "x000", "x1", -1},
		{"equal", "a01", "a01", 0},
		{"empty-equal", "", "", 0},
		{"empty-less", "", "a", -1},
		{"digit-vs-low-byte", "a1", "a-", 1},   // '1' > '-'
		{"digit-vs-high-byte", "a1", "ab", -1}, // '1' < 'b'
		{"nondigit-lengths", "a", "aa", -1},
		{"nondigit-boundary", "ab1", "abc", -1},
		{"nonascii-utf8", "文件2", "文件10", -1},
		{"nonascii-fullwidth", "１２", "３", -1},
		{"nonascii-equal", "１２", "１２", 0},
	}
	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("%s: Compare(%q, %q) = %d, want %d", tc.name, tc.a, tc.b, got, tc.want)
		}
		if got := Compare(tc.b, tc.a); got != -tc.want {
			t.Errorf("%s: Compare(%q, %q) = %d, want %d", tc.name, tc.b, tc.a, got, -tc.want)
		}
		if (tc.want < 0) != Less(tc.a, tc.b) {
			t.Errorf("%s: Less(%q, %q) inconsistent with Compare", tc.name, tc.a, tc.b)
		}
	}
}

// alphabet strings over {a,0,1,9} of length 0-4: 341 strings.
func alphabet() []string {
	ss := []string{""}
	for l := 1; l <= 4; l++ {
		var next []string
		for _, s := range ss {
			if len(s) == l-1 {
				for _, c := range []string{"a", "0", "1", "9"} {
					next = append(next, s+c)
				}
			}
		}
		ss = append(ss, next...)
	}
	return ss
}

func TestTotalOrder(t *testing.T) {
	ss := alphabet()
	n := len(ss)
	cmp := make([][]int, n)
	for i := range cmp {
		cmp[i] = make([]int, n)
		for j := range cmp[i] {
			cmp[i][j] = Compare(ss[i], ss[j])
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if cmp[i][j] != -cmp[j][i] {
				t.Fatalf("antisymmetry: Compare(%q,%q)=%d, Compare(%q,%q)=%d",
					ss[i], ss[j], cmp[i][j], ss[j], ss[i], cmp[j][i])
			}
			if (cmp[i][j] == 0) != (i == j) {
				t.Fatalf("zero only for identical strings violated: %q vs %q", ss[i], ss[j])
			}
			if cmp[i][j] >= 0 {
				continue
			}
			for k := 0; k < n; k++ {
				if cmp[j][k] < 0 && cmp[i][k] >= 0 {
					t.Fatalf("transitivity: %q < %q < %q but Compare gives %d",
						ss[i], ss[j], ss[k], cmp[i][k])
				}
			}
		}
	}
}

func TestSortDeterministic(t *testing.T) {
	base := []string{"file10", "file2", "a01", "a1", "a001", "x0", "x00",
		"x000", "x1", "a1b01", "a01b1", "a01b", "a1c", "１２", "文件2", "file2"}
	want := slices.Clone(base)
	Sort(want)
	for seed := int64(0); seed < 50; seed++ {
		s := slices.Clone(base)
		rand.New(rand.NewSource(seed)).Shuffle(len(s), func(i, j int) {
			s[i], s[j] = s[j], s[i]
		})
		Sort(s)
		if !slices.Equal(s, want) {
			t.Fatalf("seed %d: sorted order differs: %q vs %q", seed, s, want)
		}
	}
}

func TestLastCompareBytes(t *testing.T) {
	cases := []struct {
		name string
		a, b string
	}{
		{"digits", strings.Repeat("9", 100000), strings.Repeat("9", 99999) + "8"},
		{"chars", strings.Repeat("a", 100000), strings.Repeat("a", 99999) + "b"},
	}
	for _, tc := range cases {
		Compare(tc.a, tc.b)
		if got := LastCompareBytes(); got > 2*(len(tc.a)+len(tc.b)) {
			t.Errorf("%s: examined %d bytes, bound is %d",
				tc.name, got, 2*(len(tc.a)+len(tc.b)))
		}
	}
}
