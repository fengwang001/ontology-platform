// Command demo 验证双输入算子的检查点屏障对齐实现。
package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"ontology/align"
	"ontology/chanbuf"
)

func main() {
	ok := true

	// chanbuf 包判定：阻塞标志、屏障计数、FIFO 出队与回滚操作。
	s := chanbuf.New()
	r := func(n int64) chanbuf.Item { return chanbuf.Item{Kind: chanbuf.KindRecord, Key: "x", Val: n} }
	s.MarkBarrier()
	s.Push(r(1), 10)
	s.Push(r(2), 20)
	fifo := s.Blocked() && s.Barriers() == 1 && s.Len() == 2
	if q, has := s.HeadSeq(); !has || q != 10 {
		fifo = false
	}
	if it, _, has := s.Pop(); !has || it.Val != 1 {
		fifo = false
	}
	s.Trim(0)
	s.RestoreMark(false)
	fifo = fifo && s.Len() == 0 && !s.Blocked() && s.Barriers() == 0
	report("chanbuf FIFO/block/rollback", fifo, &ok)

	// align 包判定：第三节十步场景的逐片段、快照 1、三类错误与整批回滚。
	rec := func(c int, k string, v int64) chanbuf.Item {
		return chanbuf.Item{Ch: c, Kind: chanbuf.KindRecord, Key: k, Val: v}
	}
	bar := func(c int, n int64) chanbuf.Item {
		return chanbuf.Item{Ch: c, Kind: chanbuf.KindBarrier, ID: n}
	}
	ten := []chanbuf.Item{rec(0, "x", 1), rec(1, "y", 2), bar(0, 1), rec(0, "x", 10),
		rec(1, "x", 3), rec(0, "y", 20), rec(1, "y", 4), bar(1, 1),
		rec(1, "x", 5), rec(0, "x", 100)}
	a := align.New(1 << 20)
	var frags [][]string
	for _, it := range ten {
		f, err := a.Push([]chanbuf.Item{it})
		if err != nil {
			break
		}
		frags = append(frags, enc(f))
	}
	want := []string{"0x+1", "1y+2", "", "", "1x+3", "", "1y+4", "B1|0x+10|0y+20", "1x+5", "0x+100"}
	stepOK := len(frags) == 10
	for i := range want {
		if got := strings.Join(frags[i], "|"); got != want[i] {
			stepOK = false
		}
	}
	snap, hasSnap := a.Snapshot(1)
	stepOK = stepOK && hasSnap && snap["x"] == 4 && snap["y"] == 6
	report("align ten-step fragments + snapshot1", stepOK, &ok)

	st := a.State()
	report("align final state {x:119 y:26}", st["x"] == 119 && st["y"] == 26, &ok)

	bad := []struct {
		name string
		it   chanbuf.Item
		want error
	}{
		{"bad channel", rec(2, "x", 1), align.ErrInvalidElement},
		{"empty key", rec(0, "", 1), align.ErrInvalidElement},
		{"barrier order", bar(0, 2), align.ErrBarrierOrder},
	}
	errOK := true
	for _, tc := range bad {
		a2 := align.New(1 << 20)
		if _, err := a2.Push([]chanbuf.Item{tc.it}); !errors.Is(err, tc.want) {
			errOK = false
		}
	}
	small := align.New(1)
	small.Push([]chanbuf.Item{bar(0, 1)})
	if _, err := small.Push([]chanbuf.Item{rec(0, "x", 1), rec(0, "y", 1)}); !errors.Is(err, align.ErrBufferLimit) {
		errOK = false
	}
	report("align three distinct errors", errOK, &ok)

	a3 := align.New(1 << 20)
	a3.Push([]chanbuf.Item{rec(0, "x", 1), bar(0, 1)})
	before := a3.State()
	_, e1 := a3.Push([]chanbuf.Item{rec(0, "y", 9), bar(0, 5)}) // 第二条乱序
	after := a3.State()
	_, stillUseable := a3.Snapshot(0)
	report("align rejected batch leaves no trace",
		errors.Is(e1, align.ErrBarrierOrder) && len(after) == len(before) &&
			after["x"] == 1 && !stillUseable && a3.State()["y"] == 0, &ok)

	if ok {
		fmt.Println("ALL OK")
	}
}

func enc(f []chanbuf.Out) []string {
	r := make([]string, 0, len(f))
	for _, o := range f {
		if o.Kind == chanbuf.KindBarrier {
			r = append(r, "B"+strconv.FormatInt(o.ID, 10))
		} else {
			r = append(r, strconv.Itoa(o.Ch)+o.Key+"+"+strconv.FormatInt(o.Val, 10))
		}
	}
	return r
}

func report(name string, pass bool, ok *bool) {
	if pass {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		*ok = false
	}
}
