package udiff_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

func render(a, b []byte, ctx int) []byte {
	ops, _ := edit.Diff(lines.Split(a), lines.Split(b), -1) // 无上限，不会失败
	return udiff.Render(hunk.Group(ops, ctx))
}

func randDoc(r *rand.Rand) []byte {
	var sb strings.Builder
	for j := 0; j < r.Intn(15); j++ {
		fmt.Fprintf(&sb, "%d%s", r.Intn(4), []string{"\n", "\r\n"}[r.Intn(2)])
	}
	if r.Intn(3) == 0 { // 最后一行可能无换行
		return bytes.TrimRight([]byte(sb.String()), "\r\n")
	}
	return []byte(sb.String())
}

// round 断言 a→b 的补丁可解析、正向应用得 b、反向应用得 a。
func round(t *testing.T, a, b []byte, ctx int) []byte {
	p := render(a, b, ctx)
	parsed, err := udiff.Parse(p, udiff.Limits{})
	if err != nil {
		t.Fatalf("自渲染补丁解析失败: %v", err)
	}
	got, err1 := patch.Apply(a, parsed, 0)
	back, err2 := patch.Reverse(b, parsed, 0)
	if err1 != nil || err2 != nil || !bytes.Equal(got, b) || !bytes.Equal(back, a) {
		t.Fatalf("往返 %q→%q: got=%q back=%q err=%v/%v", a, b, got, back, err1, err2)
	}
	return p
}

func TestRoundtrip(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		round(t, randDoc(r), randDoc(r), r.Intn(4))
	}
}

func TestZeroCountHeads(t *testing.T) {
	ab := [][2]string{{"x\n", "y\nx\n"}, {"a\nb\nc\n", "a\nb\nX\nc\n"}, {"a\n", ""}}
	heads := []string{"@@ -0,0 +1 @@\n", "@@ -2,0 +3 @@\n", "@@ -1 +0,0 @@\n"}
	for i, c := range ab {
		if p := string(render([]byte(c[0]), []byte(c[1]), []int{0, 0, 3}[i])); !strings.Contains(p, heads[i]) {
			t.Fatalf("补丁 %q 缺少头 %q", p, heads[i])
		}
	}
}

func TestMergeThreshold(t *testing.T) {
	for _, ctx := range []int{0, 1, 3} {
		for _, g := range []int{2 * ctx, 2*ctx + 1} {
			gap := strings.Repeat("x\n", g)
			p := string(render([]byte("A\n"+gap+"B\n"), []byte("A2\n"+gap+"B2\n"), ctx))
			if n := strings.Count(p, "@@") / 2; n != map[bool]int{false: 1, true: 2}[g > 2*ctx] {
				t.Fatalf("ctx=%d g=%d: hunk 数 %d", ctx, g, n)
			}
		}
	}
}

func TestNoNewlineMark(t *testing.T) {
	p := round(t, []byte("a"), []byte("a\n"), 3)
	if !bytes.Contains(p, []byte("\\ No newline at end of file\n")) {
		t.Fatalf("仅末尾换行变化应产生带标记的非空补丁: %q", p)
	}
}
func TestParseStrict(t *testing.T) {
	bad := []string{ // 前缀为期望报错的行号
		"5|--- a\n+++ b\n@@ -1,2 +1,2 @@\n x\n",        // 少一行
		"5|--- a\n+++ b\n@@ -1 +1 @@\n x\n y\n",        // 多一行
		"4|--- a\n+++ b\n@@ -1 +1 @@\n?x\n",            // 坏行首
		"3|--- a\n+++ b\n@@ -1 +1\n x\n",               // 坏头
		"3|--- a\n+++ b\n@@ -0,1 +1 @@\n x\n",          // 行号为 0
		"4|--- a\n+++ b\n@@ -1 +1 @@\n x",              // 行尾被截
		"4|--- a\n+++ b\n@@ -1 +1 @@\n\\ No newline\n", // 坏标记
	}
	for _, c := range bad {
		_, err := udiff.Parse([]byte(c[2:]), udiff.Limits{})
		if !errors.Is(err, udiff.ErrFormat) || !strings.Contains(err.Error(), "第 "+c[:1]+" 行") {
			t.Fatalf("%s: 期望第 %c 行格式错误, got %v", c[2:], c[0], err)
		}
	}
	ok := "--- a\n+++ b\n@@ -1,2 +1,2 @@\n \n x\n" // 空行上下文（只有一个空格）
	if _, err := udiff.Parse([]byte(ok), udiff.Limits{}); err != nil {
		t.Fatalf("空上下文行不应报错: %v", err)
	}
}

func TestTruncation(t *testing.T) {
	src := []byte("x\ny\nz\nw\n")
	p := render(src, []byte("x\nz\nq\nw\n"), 1)
	for cut := 0; cut <= len(p); cut++ {
		parsed, err := udiff.Parse(p[:cut], udiff.Limits{})
		if err == nil {
			_, err = patch.Apply(src, parsed, 0)
		}
		if err != nil && !errors.Is(err, udiff.ErrFormat) &&
			!errors.Is(err, patch.ErrContext) && !errors.Is(err, patch.ErrRange) {
			t.Fatalf("cut=%d: 意外错误 %v", cut, err)
		}
	}
}

func TestFlip(t *testing.T) {
	src := []byte("x\ny\nz\nw\n")
	ls := lines.Split(render(src, []byte("x\nz\nq\nw\n"), 1))
	for i, l := range ls {
		mut := bytes.Replace(l, []byte("-1"), []byte("-9"), 1) // 头行：A 侧起点挪出范围
		if l[0] == '-' {
			mut = append([]byte("+"), l[1:]...)
		}
		if bytes.Equal(mut, l) {
			continue
		}
		joined := lines.Join(append(append([][]byte{}, ls[:i]...), append([][]byte{mut}, ls[i+1:]...)...))
		parsed, err := udiff.Parse(joined, udiff.Limits{})
		if err == nil {
			_, err = patch.Apply(src, parsed, 0)
		}
		if err == nil {
			t.Fatalf("第 %d 行翻转后应被拒绝", i+1)
		}
	}
}

func TestLimits(t *testing.T) {
	p := render([]byte("1\n2\n3\n4\n5\n6\n7\n8\n"), []byte("1\nX\n3\n4\n5\n6\n7\nY\n"), 0)
	for _, lim := range []udiff.Limits{{MaxHunks: 1}, {MaxBytes: len(p) - 1}} {
		if _, err := udiff.Parse(p, lim); !errors.Is(err, udiff.ErrLimit) {
			t.Fatalf("lim=%+v 应报 ErrLimit, got %v", lim, err)
		}
	}
}
