package main

import (
	"errors"
	"fmt"
	"os"
	"slices"

	"ontology/gcer"
)

var failed bool

func check(name string, cond bool) {
	if !cond {
		failed = true
		fmt.Println(name + ": FAIL")
		return
	}
	fmt.Println(name + ": OK")
}

func main() {
	// 第三节八步：每步后的回收上界 G
	l := gcer.New(8)
	g := make([]int64, 0, 8)
	step := func() { g = append(g, l.Watermark()) }
	for i := 0; i < 4; i++ {
		l.Append("k", "v")
	}
	step() // 1 Append×4
	s1, _ := l.Open()
	step() // 2 Open S1 (W=4)
	l.Append("k", "v")
	step()          // 3 Append (Seq5)
	_, _ = l.Open() // S2 (W=5)
	step()          // 4 Open S2 (W=5)
	l.GC()
	step() // 5 GC → G=4
	l.Append("k", "v")
	step() // 6 Append (Seq6)
	_ = l.Close(s1)
	step() // 7 Close S1
	l.GC()
	step() // 8 GC → G=5
	check(fmt.Sprintf("eight-step G=%v reclaimed={1..G}", g), slices.Equal(g, []int64{0, 0, 0, 0, 4, 4, 4, 5}))

	// 回收含等号：八步后 G=5，新开快照(W=6)探边界，Seq==G 已收，Seq=G+1 可重放
	s3, _ := l.Open()
	_, _, errEq := l.Replay(s3, 5)
	_, _, errAbove := l.Replay(s3, 6)
	check("inclusive boundary seq==G reclaimed", errors.Is(errEq, gcer.ErrReclaimed) && errAbove == nil)

	// 无活跃快照：GC 全收到当前 Seq
	l2 := gcer.New(4)
	for i := 0; i < 3; i++ {
		l2.Append("k", "v")
	}
	l2.GC()
	check("no active snapshot reclaims all", l2.Watermark() == 3)

	// Close 后重估最小水位：关掉最小水位快照，下次 GC 推进到新最小值
	l3 := gcer.New(4)
	l3.Append("k", "v")
	l3.Append("k", "v")
	a, _ := l3.Open() // W=2
	l3.Append("k", "v")
	b, _ := l3.Open() // W=3
	l3.GC()
	_ = l3.Close(a)
	l3.GC()
	_, _, errB := l3.Replay(b, 3)
	check("close re-estimates min watermark", l3.Watermark() == 3 && errors.Is(errB, gcer.ErrReclaimed))

	if failed {
		os.Exit(1)
	}
}
