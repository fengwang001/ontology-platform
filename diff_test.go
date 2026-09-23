package ontology_test

import (
	"bytes"
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"ontology/edit"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

func mkdoc(r *rand.Rand, n int) []byte {
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("l" + strconv.Itoa(r.Intn(7)))
		switch r.Intn(6) {
		case 0:
			sb.WriteString("\r\n")
		default:
			sb.WriteString("\n")
		}
	}
	s := sb.String()
	if n > 0 && r.Intn(3) == 0 {
		s = strings.TrimSuffix(strings.TrimSuffix(s, "\n"), "\r")
	}
	return []byte(s)
}

func mutate(r *rand.Rand, a []byte) []byte {
	ls := lines.Split(a)
	var out []lines.Line
	for _, l := range ls {
		switch r.Intn(8) {
		case 0: // delete
		case 1:
			out = append(out, lines.Line{Text: "m" + strconv.Itoa(r.Intn(9)), End: l.End})
		default:
			out = append(out, l)
		}
		if r.Intn(8) == 0 {
			out = append(out, lines.Line{Text: "i" + strconv.Itoa(r.Intn(9)), End: "\n"})
		}
	}
	return lines.Join(out)
}

func roundtrip(t *testing.T, a, b []byte) {
	t.Helper()
	p, err := udiff.Diff("a", "b", a, b, 3)
	if err != nil {
		t.Fatal(err)
	}
	q, err := udiff.Parse(udiff.Render(p), udiff.Limits{})
	if err != nil {
		t.Fatalf("parse own render: %v", err)
	}
	fwd, err := patch.Apply(a, q, 0)
	if err != nil || !bytes.Equal(fwd, b) {
		t.Fatalf("apply: err=%v ok=%v", err, bytes.Equal(fwd, b))
	}
	back, err := patch.Reverse(b, q, 0)
	if err != nil || !bytes.Equal(back, a) {
		t.Fatalf("reverse: err=%v ok=%v", err, bytes.Equal(back, a))
	}
}

func TestRoundtrip(t *testing.T) {
	fixed := [][2]string{{"", ""}, {"", "x\n"}, {"x\n", ""}, {"a\r\nb\r\n", "a\r\nc\r\n"},
		{"a\nb", "a\nc"}, {"a\nb\n", "a\nb"}, {"a\nb", "a\nb\n"}, {"a\r\nb\nc", "a\r\nB\nc\n"}}
	for _, c := range fixed {
		roundtrip(t, []byte(c[0]), []byte(c[1]))
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		a := mkdoc(r, r.Intn(12))
		roundtrip(t, a, mutate(r, a))
	}
}

func TestDeterministic(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	a, b := mkdoc(r, 30), mutate(r, mkdoc(r, 30))
	p, _ := udiff.Diff("a", "b", a, b, 3)
	first := udiff.Render(p)
	for i := 0; i < 100; i++ {
		if q, _ := udiff.Diff("a", "b", a, b, 3); !bytes.Equal(udiff.Render(q), first) {
			t.Fatal("render not deterministic")
		}
	}
}

func TestMinimal(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 300; i++ {
		a, b := lines.Split(mkdoc(r, r.Intn(9))), lines.Split(mkdoc(r, r.Intn(9)))
		ops, err := new(edit.Differ).Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		cost := 0
		for _, op := range ops {
			if op.Kind != edit.Equal {
				cost++
			}
		}
		if want := len(a) + len(b) - 2*lcs(a, b); cost != want {
			t.Fatalf("cost %d != minimal %d", cost, want)
		}
	}
}

func lcs(a, b []lines.Line) int {
	dp := make([][]int, len(a)+1)
	for i := range dp {
		dp[i] = make([]int, len(b)+1)
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			dp[i][j] = max(dp[i-1][j], dp[i][j-1])
			if a[i-1] == b[j-1] {
				dp[i][j] = max(dp[i][j], dp[i-1][j-1]+1)
			}
		}
	}
	return dp[len(a)][len(b)]
}

func TestHeadersAndMerge(t *testing.T) {
	heads := []struct{ a, b string; c int; want string }{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@"},
		{"a\n", "", 3, "@@ -1 +0,0 @@"},
	}
	for _, c := range heads {
		p, _ := udiff.Diff("a", "b", []byte(c.a), []byte(c.b), c.c)
		if !strings.Contains(string(udiff.Render(p)), c.want) {
			t.Fatalf("want header %s in %q", c.want, udiff.Render(p))
		}
	}
	merges := []struct{ c, g, want int }{{1, 2, 1}, {1, 3, 2}, {0, 0, 1}, {0, 1, 2}, {3, 6, 1}, {3, 7, 2}}
	for _, m := range merges {
		a := "c\nx\n" + strings.Repeat("g\n", m.g) + "y\nc\n"
		b := strings.Replace(a, "x\n", "X\n", 1)
		b = strings.Replace(b, "y\n", "Y\n", 1)
		p, _ := udiff.Diff("a", "b", []byte(a), []byte(b), m.c)
		if len(p.Hunks) != m.want {
			t.Fatalf("C=%d g=%d: got %d hunks want %d", m.c, m.g, len(p.Hunks), m.want)
		}
	}
}

func TestNoNewlineMarker(t *testing.T) {
	p, _ := udiff.Diff("a", "b", []byte("x\ny"), []byte("x\nz"), 3)
	if !strings.Contains(string(udiff.Render(p)), `\ No newline at end of file`) {
		t.Fatal("missing marker")
	}
	roundtrip(t, []byte("x\ny"), []byte("x\nz"))
	roundtrip(t, []byte("x\n"), []byte("x")) // only trailing newline differs
}

func TestComplexity(t *testing.T) {
	steps := func(n int) (int, int) {
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sb.WriteString("l" + strconv.Itoa(i) + "\n")
		}
		a := lines.Split([]byte(sb.String()))
		b := append([]lines.Line(nil), a...)
		b[n/4] = lines.Line{Text: "X", End: "\n"}
		b = append(b[:n/2], b[n/2+1:]...)
		b = append(b[:3*n/4], append([]lines.Line{{Text: "Y", End: "\n"}}, b[3*n/4:]...)...)
		d := &edit.Differ{}
		ops, err := d.Diff(a, b)
		if err != nil {
			t.Fatal(err)
		}
		dist := 0
		for _, op := range ops {
			if op.Kind != edit.Equal {
				dist++
			}
		}
		if d.Steps() > 4*(len(a)+len(b))*(dist+1) {
			t.Fatalf("n=%d steps %d over bound", n, d.Steps())
		}
		return d.Steps(), dist
	}
	s1, _ := steps(1000)
	s2, _ := steps(100000)
	if s2 > 150*s1 {
		t.Fatalf("steps %d vs %d: superlinear", s2, s1)
	}
	d := &edit.Differ{MaxDist: 1}
	if _, err := d.Diff(lines.Split([]byte("a\nb\nc\n")), lines.Split([]byte("x\ny\nz\n"))); !errors.Is(err, edit.ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}
