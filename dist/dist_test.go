package dist

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
)

func TestDistanceSamples(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"CA", "AC", 1},
		{"CA", "ABC", 2}, // unrestricted transposition, not OSA's 3
		{"ab", "ba", 1},
		{"abc", "ca", 2},
		{"", "abc", 3},
		{"abc", "", 3},
		{"", "", 0},
		{"é", "e", 1},   // é is one code point U+00E9
		{"日本", "本日", 1}, // adjacent CJK transposition
		{"\xff", "\xfe", 1},
		{"\xff", "\xff", 0},
		{"\xff", "�", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		got, err := Distance(c.a, c.b)
		if err != nil || got != c.want {
			t.Errorf("Distance(%q, %q) = %d, %v; want %d", c.a, c.b, got, err, c.want)
		}
	}
}

func enumStrings(alphabet string, maxLen int) []string {
	set := []string{""}
	for l := 1; l <= maxLen; l++ {
		var next []string
		for _, s := range set {
			if len(s) != l-1 {
				continue
			}
			for _, r := range alphabet {
				next = append(next, s+string(r))
			}
		}
		set = append(set, next...)
	}
	return set
}

func checkMetric(t *testing.T, a, b, c string) {
	t.Helper()
	dab, _ := Distance(a, b)
	dba, _ := Distance(b, a)
	dac, _ := Distance(a, c)
	dbc, _ := Distance(b, c)
	if dab != dba {
		t.Errorf("asymmetry: d(%q,%q)=%d != d(%q,%q)=%d", a, b, dab, b, a, dba)
	}
	if (dab == 0) != (a == b) {
		t.Errorf("identity violated: d(%q,%q)=%d", a, b, dab)
	}
	if dac > dab+dbc {
		t.Errorf("triangle violated: d(%q,%q)=%d > %d+%d", a, c, dac, dab, dbc)
	}
}

func TestMetricProperties(t *testing.T) {
	set := enumStrings("abc", 3) // 40 strings, exhaustive pairs and triples
	for _, a := range set {
		for _, b := range set {
			for _, c := range set {
				checkMetric(t, a, b, c)
			}
		}
	}
	r := rand.New(rand.NewSource(1)) // random triples, lengths 0-6
	for k := 0; k < 3000; k++ {
		triple := [3]string{}
		for i := range triple {
			n := r.Intn(7)
			var sb strings.Builder
			for j := 0; j < n; j++ {
				sb.WriteByte("abc"[r.Intn(3)])
			}
			triple[i] = sb.String()
		}
		checkMetric(t, triple[0], triple[1], triple[2])
	}
}

func TestCellCounter(t *testing.T) {
	for _, n := range []int{100, 1000} {
		ResetFilledCells()
		a := strings.Repeat("a", n)
		if _, err := Distance(a, a); err != nil {
			t.Fatal(err)
		}
		if got, limit := FilledCells(), int64(2*(n+1)*(n+1)); got > limit {
			t.Errorf("n=%d: filled %d cells > %d", n, got, limit)
		}
	}
}

func TestLimitError(t *testing.T) {
	old := SetMaxProduct(3)
	defer SetMaxProduct(old)
	if _, err := Distance("abcd", "wxyz"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("want ErrTooLarge, got %v", err)
	}
	if _, err := Distance("ab", "c"); err != nil {
		t.Errorf("2*1=3 within limit, got %v", err)
	}
}
