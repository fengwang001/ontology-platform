package main

import (
	"errors"
	"fmt"
	"maps"
	"sync"

	"ontology/api"
	"ontology/sink"
	"ontology/wm"
)

func tag(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAIL"
}

func r(p int, o int64, k string, v int64) wm.Rec {
	return wm.Rec{Partition: p, Offset: o, Key: k, Val: v}
}

func main() {
	// 1) 第三节三批，批2 后 Restart。
	s := api.New(8)
	s.Write([]wm.Rec{r(0, 0, "a", 1), r(0, 1, "a", 2), r(1, 0, "b", 5)})
	s.Write([]wm.Rec{r(1, 1, "b", 3), r(0, 1, "a", 2), r(0, 3, "a", 4)})
	s.Restart()
	s.Write([]wm.Rec{r(1, 1, "b", 3), r(0, 3, "a", 4), r(1, 2, "a", 6), r(0, 4, "b", 8)})
	t := s.Table()
	ok1 := t["a"] == 13 && t["b"] == 16 && s.Watermark(0) == 4 && s.Watermark(1) == 2 && s.Duplicates() == 3
	fmt.Printf("第三节10条: a=13 b=16 W=[4,2] dup=3 %s\n", tag(ok1))

	// 2) 三类错误互不相同，被拒后状态不变，之后仍可正常使用。
	s2 := api.New(1)
	e1 := s2.Write([]wm.Rec{r(-1, 0, "a", 1)})
	e2 := s2.Write([]wm.Rec{r(0, 0, "a", 1), r(0, 0, "b", 2)})
	e3 := s2.Write([]wm.Rec{r(0, 0, "a", 1), r(1, 0, "b", 2)})
	diff := errors.Is(e1, wm.ErrIllegalRec) && errors.Is(e2, wm.ErrOutOfOrder) && errors.Is(e3, sink.ErrTooManyPartitions)
	notrace := s2.Watermark(0) == -1 && s2.Watermark(1) == -1 && s2.Duplicates() == 0 && len(s2.Table()) == 0
	usable := s2.Write([]wm.Rec{r(0, 0, "a", 7)}) == nil && s2.Table()["a"] == 7
	fmt.Printf("三类错误可判定/失败不留痕/仍可用: %s\n", tag(diff && notrace && usable))

	// 3) 大 m 下重启检查个数不随 m 增长。
	fmt.Printf("重启检查个数 O(分区+Key) 不随 m 增长: %s\n", tag(sink.RebuildBoundOK([]int{100, 1000, 10000})))

	// 4) 内置自检：随机重投+任意重启与朴素参照一致、分区独立、重启无关。
	fmt.Printf("SelfCheck 朴素参照/分区独立/重启无关: %s\n", tag(api.New(8).SelfCheck() == nil))

	// 5) N 个 goroutine 并发写同一批：恰好生效一次，重复数=(N-1)*批大小。
	const N = 16
	batch := []wm.Rec{r(0, 0, "a", 1), r(0, 1, "a", 2), r(1, 0, "b", 5), r(1, 1, "b", 3)}
	c := api.New(4)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = c.Write(batch) }()
	}
	wg.Wait()
	one := api.New(4)
	_ = one.Write(batch)
	ok5 := maps.Equal(c.Table(), one.Table()) && c.Duplicates() == int64((N-1)*len(batch))
	fmt.Printf("并发%d路重复写同一批 恰好生效一次: %s\n", N, tag(ok5))
}
