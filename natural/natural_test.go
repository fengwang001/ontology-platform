package natural

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCompareBasic(t *testing.T) {
	cases := []struct{ a, b string; want int }{
		{"file2", "file10", -1},
		{"a9b", "a10a", -1},
		{"x", "x1", -1},
		{"file10", "file2", 1},
		{"abc", "abc", 0},
		{"a10", "a9", 1},
		{"", "a", -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
		if got := Less(c.a, c.b); got != (c.want < 0) {
			t.Errorf("Less(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestLargeNumbers(t *testing.T) {
	cases := []struct{ a, b string; want int }{
		{strings.Repeat("9", 40), "1" + strings.Repeat("0", 40), -1},
		{strings.Repeat("0", 40) + "1", strings.Repeat("0", 39) + "2", -1},
		{"1" + strings.Repeat("0", 1000), strings.Repeat("9", 1000), 1},
		{strings.Repeat("0", 100) + "7", strings.Repeat("0", 100) + "7", 0},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%d-digit,%d-digit)=%d want %d", len(c.a), len(c.b), got, c.want)
		}
	}
}

func TestLeadingZero(t *testing.T) {
	cases := []struct{ a, b string; want int }{
		{"a1", "a01", -1},
		{"a01", "a001", -1},
		{"a01b", "a1c", -1},
		{"a1b01", "a01b1", -1},
		{"x0", "x00", -1},
		{"x00", "x000", -1},
		{"x000", "x1", -1},
		{"a1c", "a01b", 1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func generate(alpha []byte, maxLen int) []string {
	out := []string{""}
	for l := 1; l <= maxLen; l++ {
		for v := 0; v < pow(len(alpha), l); v++ {
			buf := make([]byte, l)
			x := v
			for i := l - 1; i >= 0; i-- {
				buf[i] = alpha[x%len(alpha)]
				x /= len(alpha)
			}
			out = append(out, string(buf))
		}
	}
	return out
}

func pow(b, e int) int {
	r := 1
	for i := 0; i < e; i++ {
		r *= b
	}
	return r
}

func TestTotalOrder(t *testing.T) {
	ss := generate([]byte{'a', '0', '1', '9'}, 5)
	for i := range ss {
		if Compare(ss[i], ss[i]) != 0 {
			t.Fatalf("irreflexive diagonal at %q", ss[i])
		}
		for j := range ss {
			c := Compare(ss[i], ss[j])
			if c+Compare(ss[j], ss[i]) != 0 {
				t.Fatalf("antisymmetry broken: %q %q", ss[i], ss[j])
			}
			if (c == 0) != (ss[i] == ss[j]) {
				t.Fatalf("equality iff identical failed: %q %q", ss[i], ss[j])
			}
		}
	}
	check := func(x, y, z string) {
		t.Helper()
		if Compare(x, y) < 0 && Compare(y, z) < 0 && Compare(x, z) >= 0 {
			t.Fatalf("transitivity broken: %q %q %q", x, y, z)
		}
	}
	// Exhaustive transitivity over all triples of the length 0..3 subset.
	small := generate([]byte{'a', '0', '1', '9'}, 3)
	for i := range small {
		for j := range small {
			for k := range small {
				check(small[i], small[j], small[k])
			}
		}
	}
	// Each length-4..5 string appears in enumerated triples as every position.
	up := len(generate([]byte{'a', '0', '1', '9'}, 3))
	for ai := up; ai < len(ss); ai++ {
		an := ss[ai]
		for i := range small {
			for j := range small {
				check(an, small[i], small[j])
				check(small[i], an, small[j])
				check(small[i], small[j], an)
			}
		}
	}
}

func TestSortShuffles(t *testing.T) {
	base := []string{"file2", "file10", "a1", "a01", "a001", "a9b", "a10a",
		"x0", "x00", "x1", "", "a1b01", "a01b1", "a01b", "a1c", "0", "00"}
	ref := append([]string(nil), base...)
	Sort(ref)
	r := rand.New(rand.NewSource(42))
	for n := 0; n < 50; n++ {
		s := append([]string(nil), base...)
		r.Shuffle(len(s), func(i, j int) { s[i], s[j] = s[j], s[i] })
		Sort(s)
		if strings.Join(s, "|") != strings.Join(ref, "|") {
			t.Fatalf("shuffle %d produced a different order", n)
		}
	}
}

func TestNonASCII(t *testing.T) {
	cases := []struct{ a, b string; want int }{
		{"é1", "é10", -1},
		{"文２", "文１２", 0},
		{"a\ufffd0", "a\ufffd1", -1},
		{string([]byte{0xff, '9'}), string([]byte{0xff, '1', '0'}), -1},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestCheckedCounter(t *testing.T) {
	a := strings.Repeat("a", 100000) + "1"
	b := strings.Repeat("a", 100000) + "2"
	Compare(a, b)
	if got := Checked(); got > 2*(len(a)+len(b)) {
		t.Fatalf("checked %d exceeds bound %d", got, 2*(len(a)+len(b)))
	}
	Compare("", "")
	if Checked() != 0 {
		t.Fatalf("checked=%d, want 0", Checked())
	}
}
