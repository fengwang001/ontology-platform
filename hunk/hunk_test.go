package hunk_test

import (
	"strings"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/udiff"
)

func build(a, b string, c int) []hunk.Hunk {
	r, err := edit.Diff(lines.Split([]byte(a)), lines.Split([]byte(b)), -1)
	if err != nil {
		panic(err)
	}
	return hunk.Build(r, c)
}

func header(h hunk.Hunk) string {
	u := udiff.Hunk{OldStart: h.OldStart, OldCount: h.OldCount,
		NewStart: h.NewStart, NewCount: h.NewCount}
	s := strings.SplitN(string(udiff.Render(udiff.Patch{Hunks: []udiff.Hunk{u}})), "\n", 3)
	return s[0]
}

func TestZeroCountHeaders(t *testing.T) {
	cases := []struct {
		a, b string
		c     int
		want  string
	}{
		{"x\n", "y\nx\n", 0, "@@ -0,0 +1 @@"},
		{"a\nb\nc\n", "a\nb\nX\nc\n", 0, "@@ -2,0 +3 @@"},
		{"a\n", "", 3, "@@ -1 +0,0 @@"},
	}
	for _, c := range cases {
		hs := build(c.a, c.b, c.c)
		if len(hs) != 1 {
			t.Fatalf("want 1 hunk got %d", len(hs))
		}
		if got := header(hs[0]); got != c.want {
			t.Fatalf("header=%q want %q", got, c.want)
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	// 两处单行改动，间隔未改动行 g；C=1 阈值 2C=2。
	mk := func(g int) (string, string) {
		a := "1\n" + strings.Repeat("u\n", g) + "2\n"
		b := "X\n" + strings.Repeat("u\n", g) + "Y\n"
		return a, b
	}
	cases := []struct{ g, want int }{
		{2, 1}, // g==2C 合并
		{3, 2}, // g==2C+1 分开
	}
	for _, c := range cases {
		a, b := mk(c.g)
		if n := len(build(a, b, 1)); n != c.want {
			t.Fatalf("g=%d hunks=%d want %d", c.g, n, c.want)
		}
	}
}

func TestNoNewlineMarker(t *testing.T) {
	cases := []struct{ a, b string }{
		{"abc", "abd"},          // 双方都无尾换行
		{"a\n", "a"},            // 仅去掉末尾换行
		{"a", "a\n"},            // 仅加上末尾换行
		{"a\r\nb\r\n", "a\r\nB\r\n"},
	}
	for _, c := range cases {
		hs := build(c.a, c.b, 3)
		var buf strings.Builder
		for _, hh := range hs {
			u := udiff.Hunk{OldStart: hh.OldStart, OldCount: hh.OldCount,
				NewStart: hh.NewStart, NewCount: hh.NewCount}
			for _, e := range hh.Body {
				u.Body = append(u.Body, udiff.Entry{Kind: e.Kind, Line: e.Line,
					NoNL: !e.Line.Terminated()})
			}
			buf.Write(udiff.Render(udiff.Patch{Hunks: []udiff.Hunk{u}}))
		}
		if c.a != c.b && strings.Contains(c.a, "a") && len(hs) == 0 {
			t.Fatal("empty patch for real change")
		}
		if (c.a == "a\n" || c.a == "a") && !strings.Contains(buf.String(), "No newline") {
			t.Fatalf("missing no-newline marker:\n%s", buf.String())
		}
	}
}

func TestStrictParse(t *testing.T) {
	good := "@@ -1 +1 @@\n-x\n+y\n"
	cases := []struct {
		text string
		ok   bool
	}{
		{good, true},
		{"@@ -1,2 +1 @@\n-x\n+y\n", false}, // 声明 2 旧行只有 1
		{"@@ -1 +1 @@\nx\n+y\n", false},     // 非法前缀
		{"@@ -1 +1,2 @@\n-x\n+y\n", false},  // 声明 2 新行只有 1
		{"@@ -0,0 +1 @@\n+y\n", true},       // 纯插入
		{"@@ -1 +0,0 @@\n-x\n", true},       // 纯删除
		{"@@ -1 +1 @@\n \n+y\n", false},     // 上下文声明不符
		{"@@ -1 +1 @@\n \n", true},           // 空文件上下文（仅一个空格的行）
		{"@@ -1 +1 @@\n-x\n\\ No newline at end of file\n+y\n", true},
		{"@@ -1 +1 @@\nx", false},            // 无前缀字符
	}
	for _, c := range cases {
		_, err := udiff.Parse([]byte(c.text))
		if (err == nil) != c.ok {
			t.Fatalf("parse(%q) err=%v want ok=%v", c.text, err, c.ok)
		}
		if err != nil {
			if fe, ok := err.(*udiff.FormatError); !ok || fe.Line <= 0 {
				t.Fatalf("error lacks patch line number: %v", err)
			}
		}
	}
}
