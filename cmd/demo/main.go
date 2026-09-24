// Command demo 逐项演示时态连接的判定；全部 OK 时退出码 0。
package main

import (
	"errors"
	"fmt"
	"math/bits"
	"os"
	"slices"
	"sync"

	"ontology/api"
	"ontology/ver"
)

var failed bool

func check(label string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s: %s\n", label, map[bool]string{true: "OK", false: "FAIL"}[ok])
}
func j(k string, ts int64, seq int, v string, f bool) api.Joined {
	return api.Joined{Key: k, TS: ts, Seq: seq, Value: v, Found: f}
}
func main() {
	steps()
	boundary()
	rejects()
	logBound()
	concurrent()
	check("SelfCheck(四不变量/Flush=朴素)", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

// 第三节十三步：逐步输出与第 9、10、13 步判定。
func steps() {
	e := api.New(10)
	var got [14][]api.Joined
	e.Upsert("k", 10, "A")          // 1
	got[2], _ = e.Feed("k", 12)     // 2
	e.Upsert("k", 20, "B")          // 3
	got[4], _ = e.Feed("k", 25)     // 4
	got[5], _ = e.Watermark(11)     // 5
	e.Upsert("k", 12, "C")          // 6
	got[7], _ = e.Feed("k", 22)     // 7
	e.Delete("k", 22)               // 8
	got[9], _ = e.Watermark(22)     // 9
	err10 := e.Upsert("k", 22, "X") // 10
	got[11], _ = e.Feed("k", 8)     // 11
	e.Upsert("k", 30, "D")          // 12
	got[13], _ = e.Watermark(30)    // 13
	want := map[int][]api.Joined{9: {j("k", 12, 0, "C", true), j("k", 22, 2, "", false)}, 11: {j("k", 8, 3, "", false)}, 13: {j("k", 25, 1, "", false)}}
	ok := true
	for i := 1; i <= 13; i++ {
		ok = ok && slices.Equal(got[i], want[i])
	}
	check("十三步每步输出", ok)
	v9 := len(got[9]) == 2 && got[9][0].Value == "C" && got[9][0].Found && !got[9][1].Found
	v13 := len(got[13]) == 1 && !got[13][0].Found
	check("step9/10/13 判定", v9 && err10 == api.ErrLate && v13)
}
func boundary() {
	e := api.New(4)
	e.Upsert("k", 10, "A")
	e.Watermark(10)
	out1, _ := e.Feed("k", 10) // TS == ValidFrom 且 TS == vwm：立即输出，取到 A
	b1 := len(out1) == 1 && out1[0].Found && out1[0].Value == "A"
	e.Delete("k", 20)
	e.Watermark(20)
	out2, _ := e.Feed("k", 20) // 墓碑区间起点：无值
	b2 := len(out2) == 1 && !out2[0].Found
	check("边界 TS==ValidFrom==vwm", b1)
	check("墓碑之后查询无值", b2)
}
func rejects() {
	e := api.New(1)
	e.Watermark(5)
	imm, _ := e.Feed("k", 3) // 立即输出 seq0
	got := []error{e.Upsert("", 6, "x"), e.Upsert("k", 5, "x")}
	_, errWm := e.Watermark(4)
	e.Feed("k", 100) // 进缓冲 seq1
	_, errBuf := e.Feed("k", 101)
	got = append(got, errWm, errBuf)
	want := []error{api.ErrEmptyKey, api.ErrLate, api.ErrWmBack, api.ErrBufFull}
	ok := len(imm) == 1
	for i, g := range got {
		for w, wantErr := range want {
			ok = ok && errors.Is(g, wantErr) == (w == i) // 各自命中且互不相同
		}
	}
	check("四类可判定错误", ok)
	before := e.Outputs()
	e.Delete("", 1)       // 拒绝：空 Key
	e.Upsert("k", 5, "y") // 拒绝：迟到
	e.Watermark(4)        // 拒绝：回退
	e.Feed("k", 101)      // 拒绝：缓冲满
	ok = slices.Equal(before, e.Outputs())
	e.Watermark(200)           // 释放缓冲中的 seq1
	out, _ := e.Feed("k", 180) // 立即输出，序号应为 2（被拒事件不占号）
	ok = ok && len(out) == 1 && out[0].Seq == 2 && len(e.Outputs()) == 3
	check("被拒后状态不变", ok)
}
func logBound() {
	const m = 10000
	s := &ver.Store{}
	for i := 0; i < m; i++ {
		s.Upsert(int64(i), "v")
	}
	bound := bits.Len(uint(m)) + 3 // bits.Len(m) == ⌈log2(m+1)⌉
	ok := true
	for _, ts := range []int64{-1, 0, 1, 5000, 9999, 10000, 123456} {
		s.AsOf(ts)
		ok = ok && s.CheckedWithin(bound)
	}
	check("大m检查个数<=对数上界", ok)
}
func concurrent() {
	const N, M = 8, 25
	e := api.New(N * M)
	for i := 0; i < 10; i++ {
		e.Upsert("k", int64(i), "v")
	}
	var wg sync.WaitGroup
	for g := 0; g < N; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < M; i++ {
				e.Feed("k", int64(g*M+i))
			}
		}(g)
	}
	wg.Wait()
	e.Flush()
	outs := e.Outputs()
	seen := make([]bool, N*M)
	ok := len(outs) == N*M
	for _, o := range outs {
		if o.Seq < 0 || o.Seq >= N*M || seen[o.Seq] || !o.Found || o.Value != "v" {
			ok = false
			continue
		}
		seen[o.Seq] = true
	}
	check("并发Feed恰好一次", ok)
}
