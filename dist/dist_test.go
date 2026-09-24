package dist_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/dist"
)

func TestDistanceTable(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"CA", "AC", 1}, {"CA", "ABC", 2}, {"ab", "ba", 1},
		{"abc", "ca", 2}, {"", "abc", 3}, {"abc", "", 3}, {"", "", 0},
		{"é", "e", 1}, {"日本", "本日", 1},
		{"\xff", "\xfe", 1}, {"\xff", "\xff", 0},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		d, err := dist.Distance(c.a, c.b, 0)
		if err != nil || d != c.want {
			t.Errorf("d(%q,%q)=%d,%v want %d", c.a, c.b, d, err, c.want)
		}
	}
}

func gen(alphabet []rune, maxLen int) []string {
	var out []string
	var rec func(cur []rune)
	rec = func(cur []rune) {
		out = append(out, string(cur))
		if len(cur) == maxLen {
			return
		}
		for _, r := range alphabet {
			rec(append(cur, r))
		}
	}
	rec(nil)
	return out
}

func TestSymmetryIdentity(t *testing.T) {
	strs := gen([]rune("abc"), 4)
	for _, a := range strs {
		for _, b := range strs {
			ab, _ := dist.Distance(a, b, 0)
			ba, _ := dist.Distance(b, a, 0)
			if ab != ba {
				t.Fatalf("asymmetry: d(%q,%q)=%d d(%q,%q)=%d", a, b, ab, b, a, ba)
			}
			if (ab == 0) != (a == b) {
				t.Fatalf("identity: d(%q,%q)=%d", a, b, ab)
			}
		}
	}
}

func checkTriangle(t *testing.T, a, b, c string) {
	t.Helper()
	ab, _ := dist.Distance(a, b, 0)
	bc, _ := dist.Distance(b, c, 0)
	ac, _ := dist.Distance(a, c, 0)
	if ac > ab+bc {
		t.Fatalf("triangle: d(%q,%q)=%d > %d+%d", a, c, ac, ab, bc)
	}
}

func TestTriangleInequality(t *testing.T) {
	strs := gen([]rune("abc"), 3)
	for _, a := range strs {
		for _, b := range strs {
			for _, c := range strs {
				checkTriangle(t, a, b, c)
			}
		}
	}
	rng := rand.New(rand.NewSource(1))
	for k := 0; k < 500; k++ {
		checkTriangle(t, randStr(rng), randStr(rng), randStr(rng))
	}
}

func randStr(rng *rand.Rand) string {
	n := rng.Intn(7)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteByte(byte('a' + rng.Intn(3)))
	}
	return sb.String()
}

func TestLimit(t *testing.T) {
	if _, err := dist.Compute("aaaa", "bbbb", 15); !errors.Is(err, dist.ErrLimitExceeded) {
		t.Fatalf("want ErrLimitExceeded, got %v", err)
	}
	if _, err := dist.Compute("aaaa", "bbbb", 16); err != nil {
		t.Fatalf("product 16 within limit 16: %v", err)
	}
}

func TestCells(t *testing.T) {
	for _, n := range []int{100, 1000} {
		a := strings.Repeat("ab", n/2)
		b := strings.Repeat("ba", n/2)
		if _, err := dist.Distance(a, b, 0); err != nil {
			t.Fatal(err)
		}
		if got, lim := dist.Cells(), 2*(n+1)*(n+1); got > lim {
			t.Fatalf("n=%d cells=%d > %d", n, got, lim)
		}
	}
}

func TestInvalidByteRoundTrip(t *testing.T) {
	s := "a\xffb\xfe\x80"
	if got := dist.Encode(dist.Decode(s)); got != s {
		t.Fatalf("round trip %q != %q", got, s)
	}
}
