package natural

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCompareTable(t *testing.T) {
	big38 := strings.Repeat("9", 38)
	cases := []struct {
		a, b string
		want int
	}{
		{"file2", "file10", -1},
		{"a9b", "a10a", -1},
		{"x", "x1", -1},
		{"", "", 0},
		{"", "a", -1},
		{"abc", "abc", 0},
		{"n" + big38 + "98", "n" + big38 + "99", -1},
		{"n" + big38 + "99", "n1" + big38 + "90", -1},
		{"a1", "a01", -1},
		{"a01", "a001", -1},
		{"a01b", "a1c", -1},
		{"a1b01", "a01b1", -1},
		{"x0", "x00", -1},
		{"x00", "x000", -1},
		{"x000", "x1", -1},
		{"文件2", "文件10", -1},
		{"１２", "１２", 0},
		{"１２", "１３", -1},
		{"a２", "a2", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := Compare(c.b, c.a); got != -c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
		if (c.want == 0) != (c.a == c.b) {
			t.Errorf("Compare(%q, %q) == 0 inconsistent with equality", c.a, c.b)
		}
	}
}

func genStrings(alphabet string, maxLen int) []string {
	out := []string{""}
	for n := 1; n <= maxLen; n++ {
		var rec func(prefix string)
		rec = func(prefix string) {
			if len(prefix) == n {
				out = append(out, prefix)
				return
			}
			for i := 0; i < len(alphabet); i++ {
				rec(prefix + alphabet[i:i+1])
			}
		}
		rec("")
	}
	return out
}

func corpus() []string {
	ss := genStrings("a019", 3)
	return append(ss, "a01b", "a1c", "a1b01", "a01b1", "x0", "x00",
		"x000", "x1", "file2", "file10", "0", "00", "01", "1", "10")
}

func TestTotalOrderExhaustive(t *testing.T) {
	ss := corpus()
	n := len(ss)
	cmp := make([][]int, n)
	for i := range cmp {
		cmp[i] = make([]int, n)
		for j := range cmp[i] {
			cmp[i][j] = Compare(ss[i], ss[j])
			if cmp[i][j] != -Compare(ss[j], ss[i]) {
				t.Fatalf("antisymmetry violated: %q vs %q", ss[i], ss[j])
			}
			if (cmp[i][j] == 0) != (ss[i] == ss[j]) {
				t.Fatalf("zero iff equal violated: %q vs %q", ss[i], ss[j])
			}
		}
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			for k := 0; k < n; k++ {
				if cmp[i][j] < 0 && cmp[j][k] < 0 && cmp[i][k] >= 0 {
					t.Fatalf("transitivity violated: %q < %q < %q",
						ss[i], ss[j], ss[k])
				}
			}
		}
	}
}

func TestSortDeterministic(t *testing.T) {
	base := corpus()
	want := append([]string(nil), base...)
	Sort(want)
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		got := append([]string(nil), base...)
		r.Shuffle(len(got), func(i, j int) { got[i], got[j] = got[j], got[i] })
		Sort(got)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("trial %d: position %d: %q != %q", trial, i, got[i], want[i])
			}
		}
	}
}

func TestCompareByteBudget(t *testing.T) {
	cases := []struct{ a, b string }{
		{strings.Repeat("x", 99999) + "a", strings.Repeat("x", 99999) + "b"},
		{strings.Repeat("1", 99999) + "3", strings.Repeat("1", 99999) + "4"},
		{strings.Repeat("0", 50000) + strings.Repeat("7", 49999) + "a",
			strings.Repeat("0", 50000) + strings.Repeat("7", 49999) + "b"},
	}
	for _, c := range cases {
		Compare(c.a, c.b)
		if got, limit := LastCompareBytes(), int64(2*(len(c.a)+len(c.b))); got > limit {
			t.Errorf("examined %d bytes, limit %d", got, limit)
		}
	}
}
