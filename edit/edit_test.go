package edit_test

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
)

func mkLines(s string) []lines.Line { return lines.Split([]byte(s)) }

func TestLinesRoundTrip(t *testing.T) {
	cases := []string{"", "a", "a\n", "a\nb", "a\r\nb\r\n", "a\n\rb", "\n\n", "x\r\n"}
	for _, in := range cases {
		got := lines.Join(lines.Split([]byte(in)))
		if !bytes.Equal(got, []byte(in)) {
			t.Errorf("roundtrip %q = %q", in, got)
		}
	}
}

func lcsDP(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if lines.Equal(a[i], b[j]) {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	return dp[0][0]
}

func TestShortestRandom(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	words := []string{"a\n", "b\n", "c\n", "d\n"}
	for iter := 0; iter < 300; iter++ {
		gen := func() []lines.Line {
			n := r.Intn(9)
			var sb strings.Builder
			for i := 0; i < n; i++ {
				sb.WriteString(words[r.Intn(len(words))])
			}
			return mkLines(sb.String())
		}
		a, b := gen(), gen()
		s, err := edit.Diff(a, b, -1, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := len(a) + len(b) - 2*lcsDP(a, b)
		if edit.Distance(s) != want {
			t.Fatalf("iter %d: distance %d want %d", iter, edit.Distance(s), want)
		}
	}
}

func TestDeterministic(t *testing.T) {
	a, b := mkLines("a\nb\n"), mkLines("b\na\n")
	var first []edit.Step
	for i := 0; i < 100; i++ {
		s, err := edit.Diff(a, b, -1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = s
			continue
		}
		if len(s) != len(first) {
			t.Fatal("length differs")
		}
		for k := range s {
			if s[k].Op != first[k].Op || !lines.Equal(s[k].Line, first[k].Line) {
				t.Fatalf("script differs at %d", k)
			}
		}
	}
	if got := ops(first); got != "-+" {
		t.Fatalf("delete-first ordering got %q", got)
	}
}

func ops(s []edit.Step) string {
	var b strings.Builder
	for _, st := range s {
		if st.Op != edit.Equal {
			b.WriteByte(st.Op)
		}
	}
	return b.String()
}

func TestMaxDistance(t *testing.T) {
	cases := []struct {
		a, b string
		cap  int
		err  bool
	}{
		{"a\n", "a\n", 0, false},
		{"a\n", "b\n", 0, true},
		{"a\nb\n", "b\na\n", 2, false},
		{"a\nb\n", "c\nd\n", 3, true},
	}
	for _, tc := range cases {
		_, err := edit.Diff(mkLines(tc.a), mkLines(tc.b), tc.cap, nil)
		if (err == edit.ErrTooDifferent) != tc.err {
			t.Errorf("cap=%d %v->%v err=%v want err=%v", tc.cap, tc.a, tc.b, err, tc.err)
		}
	}
}

func buildFile(n int, change bool) []lines.Line {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		if change && (i == n/4 || i == n/2 || i == 3*n/4) {
			sb.WriteString(fmt.Sprintf("z%d\n", i))
			continue
		}
		sb.WriteString(fmt.Sprintf("line%d\n", i))
	}
	return mkLines(sb.String())
}

func TestCounterScale(t *testing.T) {
	var counts []int
	for _, n := range []int{1000, 100000} {
		var c edit.Counter
		_, err := edit.Diff(buildFile(n, false), buildFile(n, true), -1, &c)
		if err != nil {
			t.Fatal(err)
		}
		if c.Steps > 4*(2*n+12) {
			t.Fatalf("n=%d counter %d exceeds 4(N+M)(D+1)", n, c.Steps)
		}
		counts = append(counts, c.Steps)
	}
	if counts[1] > 150*counts[0] {
		t.Fatalf("scale ratio %d/%d too large", counts[1], counts[0])
	}
}

func TestZeroCountHeads(t *testing.T) {
	cases := []struct {
		a, b, want string
	}{
		{"x\n", "y\nx\n", "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", "@@ -2,0 +3 @@"},
		{"a\n", "", "@@ -1 +0,0 @@"},
	}
	for _, tc := range cases {
		s, _ := edit.Diff(mkLines(tc.a), mkLines(tc.b), -1, nil)
		hs := hunk.Build(s, 0)
		if len(hs) != 1 {
			t.Fatalf("want 1 hunk got %d", len(hs))
		}
		got := fmt.Sprintf("@@ -%s +%s @@", num(hs[0].OldStart, hs[0].OldCount), num(hs[0].NewStart, hs[0].NewCount))
		if got != tc.want {
			t.Errorf("%v->%v head %q want %q", tc.a, tc.b, got, tc.want)
		}
	}
}

func num(s, c int) string {
	if c == 1 {
		return fmt.Sprintf("%d", s)
	}
	return fmt.Sprintf("%d,%d", s, c)
}

func TestMergeThreshold(t *testing.T) {
	mk := func(g, ctx int) int {
		a := "A\n" + strings.Repeat("x\n", g) + "B\n"
		b := "A1\n" + strings.Repeat("x\n", g) + "B1\n"
		s, _ := edit.Diff(mkLines(a), mkLines(b), -1, nil)
		return len(hunk.Build(s, ctx))
	}
	if got := mk(2, 1); got != 1 {
		t.Errorf("g=2,C=1 hunks=%d want 1 (merge)", got)
	}
	if got := mk(3, 1); got != 2 {
		t.Errorf("g=3,C=1 hunks=%d want 2 (split)", got)
	}
	if got := mk(6, 3); got != 1 {
		t.Errorf("g=6,C=3 hunks=%d want 1", got)
	}
	if got := mk(7, 3); got != 2 {
		t.Errorf("g=7,C=3 hunks=%d want 2", got)
	}
}
