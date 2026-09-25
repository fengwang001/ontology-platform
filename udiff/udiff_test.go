package udiff_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"ontology/patch"
	"ontology/udiff"
	"strings"
	"testing"
)

func roundTrip(t *testing.T, a, b string) {
	t.Helper()
	hs, err := udiff.Diff([]byte(a), []byte(b), 3, -1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := udiff.Parse(udiff.Render("a", "b", hs), udiff.Limits{})
	if err != nil {
		t.Fatalf("parse own render: %v", err)
	}
	out, err := patch.Apply([]byte(a), p, 0)
	if err != nil || string(out) != b {
		t.Fatalf("apply(%q) = %q, %v; want %q", a, out, err, b)
	}
	back, err := patch.Reverse([]byte(b), p, 0)
	if err != nil || string(back) != a {
		t.Fatalf("reverse(%q) = %q, %v; want %q", b, back, err, a)
	}
}

func TestRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"", ""}, {"a\n", "a\n"}, {"", "a\n"}, {"a\n", ""},
		{"a\nb\nc\n", "a\nX\nc\n"}, {"x\n", "y\nx\n"},
		{"a\r\nb\r\nc\r\n", "a\r\nX\r\nc\r\n"}, // CRLF 保留
		{"a\nb", "a\nb\n"}, {"a\nb\n", "a\nb"}, // 末尾换行增删
		{"x", "y"}, {"one", "two\nthree"},
		{"l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nl8\n", "A\nl1\nl2\nl3\nl4\nl5\nl6\nl7\nB\n"},
	}
	rng := rand.New(rand.NewSource(3))
	for i := 0; i < 200; i++ {
		var a, b strings.Builder
		for j := 0; j < rng.Intn(12); j++ {
			fmt.Fprintf(&a, "l%d%s", rng.Intn(5), []string{"\n", "\r\n", ""}[rng.Intn(3)])
		}
		for j := 0; j < rng.Intn(12); j++ {
			fmt.Fprintf(&b, "l%d%s", rng.Intn(5), []string{"\n", "\r\n", ""}[rng.Intn(3)])
		}
		cases = append(cases, [2]string{a.String(), b.String()})
	}
	for _, tc := range cases {
		roundTrip(t, tc[0], tc[1])
	}
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		old, new string
		ctx      int
		want     string
	}{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@\n"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@\n"},
		{"a\n", "", 3, "@@ -1 +0,0 @@\n"},
	}
	for _, tc := range cases {
		hs, err := udiff.Diff([]byte(tc.old), []byte(tc.new), tc.ctx, -1)
		if err != nil {
			t.Fatal(err)
		}
		if got := udiff.Render("a", "b", hs); !bytes.Contains(got, []byte(tc.want)) {
			t.Fatalf("%q -> %q: header missing %q in\n%s", tc.old, tc.new, tc.want, got)
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	for _, c := range []int{1, 2, 3} {
		for _, tc := range []struct{ gap, want int }{{2 * c, 1}, {2*c + 1, 2}} {
			n := 2*c + 2 + tc.gap + 1
			var a []string
			for i := 0; i < n; i++ {
				a = append(a, fmt.Sprintf("l%d\n", i))
			}
			b := append([]string(nil), a...)
			b[c], b[c+1+tc.gap] = "X\n", "Y\n"
			hs, err := udiff.Diff([]byte(strings.Join(a, "")), []byte(strings.Join(b, "")), c, -1)
			if err != nil {
				t.Fatal(err)
			}
			if len(hs) != tc.want {
				t.Fatalf("C=%d gap=%d: %d hunks, want %d", c, tc.gap, len(hs), tc.want)
			}
		}
	}
}

func TestNoNewline(t *testing.T) {
	hs, err := udiff.Diff([]byte("a\nb"), []byte("a\nb\n"), 3, -1)
	if err != nil {
		t.Fatal(err)
	}
	p := udiff.Render("a", "b", hs)
	if len(hs) == 0 || !bytes.Contains(p, []byte("\\ No newline at end of file\n")) {
		t.Fatalf("newline-only change must produce marked patch:\n%s", p)
	}
	roundTrip(t, "a\nb", "a\nb\n")
	roundTrip(t, "a\nb\n", "a\nb")
}

func TestDeterministic(t *testing.T) {
	a, b := []byte("x\ny\nz\n"), []byte("y\nz\nx\n")
	first, err := udiff.Diff(a, b, 3, -1)
	if err != nil {
		t.Fatal(err)
	}
	want := udiff.Render("a", "b", first)
	for i := 0; i < 100; i++ {
		hs, _ := udiff.Diff(a, b, 3, -1)
		if got := udiff.Render("a", "b", hs); !bytes.Equal(got, want) {
			t.Fatalf("run %d differs:\n%s", i, got)
		}
	}
}

func TestParseStrict(t *testing.T) {
	bad := []string{
		"+++ b\n--- a\n",                           // 头部顺序错
		"--- a\n+++ b\n@@ -1,2 +1 @@\n-a\n",        // 行数不足
		"--- a\n+++ b\n@@ -1 +1 @@\n-a\n+b\n-c\n",  // 行数过多
		"--- a\n+++ b\n@@ -1 +1 @@\n?a\n",          // 非法行首
		"--- a\n+++ b\n@@ -1 @@\n-a\n",             // 头缺 + 段
		"--- a\n+++ b\n@@ -x +1 @@\n",              // 头含非数字
		"--- a\n+++ b\n@@ -1 +1 @@\n-a",            // 内容行截断
		"--- a\n+++ b\n@@ -1 +1 @@\n-a\n\\ oops\n", // 标记后行数仍不足
	}
	for _, src := range bad {
		var le *udiff.LineError
		if err := mustErr(src); !errors.As(err, &le) || le.Line < 1 {
			t.Fatalf("%q: want LineError with line, got %v", src, err)
		}
	}
	// 单空格上下文行（空行内容）合法：旧 "a\n\n" → 新 "b\n\n"
	good := "--- a\n+++ b\n@@ -1,2 +1,2 @@\n-a\n+b\n \n"
	p, err := udiff.Parse([]byte(good), udiff.Limits{})
	if err != nil || len(p.Hunks) != 1 || len(p.Hunks[0].Lines) != 3 {
		t.Fatalf("empty context line: %v", err)
	}
}

func mustErr(src string) error {
	_, err := udiff.Parse([]byte(src), udiff.Limits{})
	if err == nil {
		return fmt.Errorf("parsed ok")
	}
	return err
}
