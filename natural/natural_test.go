package natural

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCompare(t *testing.T) {
	cases := []struct {
	a, b string
		want int
	}{
		{"file2", "file10", -1},
		{"a9b", "a10a", -1},
		{"x", "x1", -1},
		{"file10", "file2", 1},
		{"abc", "abc", 0},
		{"a1", "a01", -1},
		{"a01", "a001", -1},
		{"a001", "a1", 1},
		{"a01b", "a1c", -1},
		{"a1c", "a01b", 1},
		{"a1b01", "a01b1", -1},
		{"x0", "x00", -1},
		{"x00", "x000", -1},
		{"x000", "x1", -1},
		{"1", "a", -1},
		{"a", "1", 1},
		{"a" + strings.Repeat("9", 40), "a" + "1"+strings.Repeat("0", 39), 1},
		{strings.Repeat("0", 39) + "1", "1", 1},
		{"文件１２", "文件2", 1},
	}
	for _, tc := range cases {
		t.Run(tc.a+"vs"+tc.b, func(t *testing.T) {
			if got := Compare(tc.a, tc.b); got != tc.want {
				t.Fatalf("Compare = %d, want %d", got, tc.want)
			}
			if -Compare(tc.b, tc.a) != tc.want {
				t.Fatalf("not antisymmetric for %q %q", tc.a, tc.b)
			}
		})
	}
}

func enumerate() []string {
	alpha := []byte{'a', '0', '1', '9'}
	out := []string{""}
	for n := 1; n <= 3; n++ {
		total := 1
		for range n {
			total *= len(alpha)
		}
		for v := 0; v < total; v++ {
			b := make([]byte, n)
			x := v
			for i := n - 1; i >= 0; i-- {
				b[i] = alpha[x%len(alpha)]
				x /= len(alpha)
			}
			out = append(out, string(b))
		}
	}
	return out
}

func TestTotalOrder(t *testing.T) {
	w := enumerate()
	s := make([][]int8, len(w))
	for i := range s {
		s[i] = make([]int8, len(w))
		for j := range w {
			s[i][j] = int8(Compare(w[i], w[j]))
		}
	}
	for i := range w {
		for j := range w {
			if s[i][j] != -s[j][i] {
				t.Fatalf("antisymmetry: %q %q", w[i], w[j])
			}
			if (s[i][j] == 0) != (w[i] == w[j]) {
				t.Fatalf("zero iff equal: %q %q", w[i], w[j])
			}
		}
	}
	for i := range w {
		for j := range w {
			for k := range w {
				if s[i][j] < 0 && s[j][k] < 0 && s[i][k] >= 0 {
					t.Fatalf("transitivity: %q <%q <%q", w[i], w[j], w[k])
				}
			}
		}
	}
}

func TestSortDeterministic(t *testing.T) {
	base := append(enumerate(), "file10", "file2", "x00", "x0")
	ref := append([]string(nil), base...)
	Sort(ref)
	cases := []int64{1, 2, 3, 50}
	for _, seed := range cases {
		t.Run("seed", func(t *testing.T) {
			got := append([]string(nil), base...)
			r := rand.New(rand.NewSource(seed))
			r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
			Sort(got)
			for i := range ref {
				if got[i] != ref[i] {
					t.Fatalf("seed %d: %q != %q at %d", seed, got[i], ref[i], i)
				}
			}
		})
	}
}

func TestByteBudget(t *testing.T) {
	cases := []struct {
		a, b string
		less bool
	}{
		{strings.Repeat("a", 100000), strings.Repeat("a", 99999) + "b", true},
		{"z" + strings.Repeat("0", 99999), "z" + strings.Repeat("0", 99998) + "1", true},
	}
	for _, tc := range cases {
		Compare(tc.a, tc.b)
		if BytesCompared() > 2*(len(tc.a)+len(tc.b)) {
			t.Fatalf("examined %d bytes, bound %d", BytesCompared(), 2*(len(tc.a)+len(tc.b)))
		}
	}
}
