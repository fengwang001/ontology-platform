package patch_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/edit"
	"ontology/hunk"
	"ontology/lines"
	"ontology/patch"
	"ontology/udiff"
)

func mkPatch(aStart int) *udiff.Patch {
	return &udiff.Patch{Hunks: []hunk.Hunk{{
		AStart: aStart, ACount: 2, BStart: 1, BCount: 2,
		Lines: []hunk.Line{
			{Kind: ' ', Text: []byte("a\n")}, {Kind: '-', Text: []byte("b\n")}, {Kind: '+', Text: []byte("B\n")},
		},
	}}}
}

func TestOffsetNearest(t *testing.T) {
	src := []byte("a\nb\na\nb\na\nb\n") // 候选位置 0、2、4
	for _, c := range [][2]int{{5, 4}, {4, 2}, {3, 2}, {1, 0}} {
		got, err := patch.Apply(src, mkPatch(c[0]), 5)
		if err != nil {
			t.Fatal(err)
		}
		ls := lines.Split(got)
		if string(ls[c[1]+1]) != "B\n" {
			t.Fatalf("记录 %d: 期望落在 %d, got %q", c[0], c[1], got)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	if _, err := edit.Diff(lines.Split([]byte("a\nb\nc\nd\n")), lines.Split([]byte("w\nx\ny\nz\n")), 1); !errors.Is(err, edit.ErrTooBig) {
		t.Fatalf("差异过大: %v", err)
	}
	if _, err := udiff.Parse([]byte("--- a\n+++ b\n@@ bad\n"), udiff.Limits{}); !errors.Is(err, udiff.ErrFormat) {
		t.Fatalf("格式错误: %v", err)
	}
	none := mkPatch(1)
	none.Hunks[0].Lines[0].Text = []byte("zzz\n")
	if _, err := patch.Apply([]byte("a\nb\n"), none, 2); !errors.Is(err, patch.ErrContext) {
		t.Fatalf("上下文不匹配: %v", err)
	}
	if _, err := patch.Apply([]byte("x\ny\nz\na\nb\n"), mkPatch(1), 1); !errors.Is(err, patch.ErrRange) {
		t.Fatalf("超出偏移范围: %v", err)
	}
}

func TestAtomic(t *testing.T) {
	p := &udiff.Patch{Hunks: []hunk.Hunk{
		{AStart: 1, ACount: 1, BStart: 1, BCount: 1, Lines: []hunk.Line{
			{Kind: '-', Text: []byte("x\n")}, {Kind: '+', Text: []byte("X\n")}}},
		{AStart: 2, ACount: 1, BStart: 2, BCount: 1, Lines: []hunk.Line{
			{Kind: '-', Text: []byte("not-here\n")}, {Kind: '+', Text: []byte("Y\n")}}},
	}}
	st := patch.NewStore()
	st.Put("d", []byte("x\ny\n"))
	v, err := st.Apply("d", p, 0)
	if err == nil || !strings.Contains(err.Error(), "第 2 个 hunk") {
		t.Fatalf("应指出第 2 个 hunk 失败: %v", err)
	}
	got, gv := st.Get("d")
	if v != 0 || gv != 0 || string(got) != "x\ny\n" {
		t.Fatalf("失败后状态应零变化: v=%d gv=%d got=%q", v, gv, got)
	}
}

func TestConcurrent(t *testing.T) {
	const N = 16
	var init strings.Builder
	for i := 0; i < 20; i++ {
		fmt.Fprintf(&init, "l%02d\n", i)
	}
	st := patch.NewStore()
	st.Put("doc", []byte(init.String()))
	mk := func(line int) *udiff.Patch {
		return &udiff.Patch{Hunks: []hunk.Hunk{{
			AStart: line + 1, ACount: 1, BStart: line + 1, BCount: 1,
			Lines: []hunk.Line{
				{Kind: '-', Text: []byte(fmt.Sprintf("l%02d\n", line))},
				{Kind: '+', Text: []byte(fmt.Sprintf("M%02d\n", line))},
			},
		}}}
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	var okN, failN atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			if _, err := st.Apply("doc", mk(i/2), 2); err != nil {
				failN.Add(1)
			} else {
				okN.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	if okN.Load()+failN.Load() != N {
		t.Fatalf("成功+失败 != N: %d+%d", okN.Load(), failN.Load())
	}
	final, ver := st.Get("doc")
	if ver != int(okN.Load()) {
		t.Fatalf("版本号 %d != 成功数 %d", ver, okN.Load())
	}
	replay := []byte(init.String())
	for _, c := range st.Log() {
		var err error
		if replay, err = patch.Apply(replay, c.Patch, c.Fuzz); err != nil {
			t.Fatalf("重放失败: %v", err)
		}
	}
	if !bytes.Equal(replay, final) {
		t.Fatalf("并发结果 != 串行重放:\n%q\n%q", final, replay)
	}
}
