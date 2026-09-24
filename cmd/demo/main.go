package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/slot"
	"ontology/wal"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func rec(lsn int64, k wal.Kind, xid, data string) wal.Record { return wal.Rec(lsn, k, xid, data) }

func main() {
	// 第三节 8 步
	a := api.New(10)
	var cr [8][2]int64
	snap := func(i int) { cr[i] = [2]int64{a.ConfirmedLSN(), a.RestartLSN()} }
	a.Append(rec(10, wal.Begin, "T1", ""), rec(20, wal.Begin, "T2", ""), rec(25, wal.Change, "T2", "x"), rec(30, wal.Commit, "T1", ""))
	snap(0)
	a.Confirm(30)
	snap(1)
	a.Append(rec(40, wal.Begin, "T3", ""), rec(50, wal.Commit, "T3", ""))
	snap(2)
	errE4 := a.Confirm(45)
	snap(3)
	a.Append(rec(60, wal.Begin, "T4", ""), rec(70, wal.Abort, "T4", ""), rec(75, wal.Change, "T2", "y"), rec(80, wal.Commit, "T2", ""))
	snap(4)
	a.Confirm(50)
	snap(5)
	a.Restart()
	snap(6)
	em := a.Emitted()
	a.Confirm(80)
	snap(7)
	want := [8][2]int64{{5, 5}, {30, 20}, {30, 20}, {30, 20}, {30, 20}, {50, 20}, {50, 20}, {80, 80}}
	fmt.Println("E1-E8 confirmed/restart:", cr)
	check("8步 confirmed/restart", cr == want)
	check("E4拒绝为确认非事务边界", errors.Is(errE4, slot.ErrNotBoundary))
	check("E7重发T2@80[x y]", len(em) == 1 && em[0].Xid == "T2" && em[0].CommitLSN == 80 &&
		len(em[0].Changes) == 2 && em[0].Changes[0] == "x" && em[0].Changes[1] == "y")

	// 四不变量自检：朴素参照一致 / 可恢复 / 单调有序 / 失败不留痕
	check("SelfCheck四不变量", api.New(10).SelfCheck() == nil)

	// 四类可判定错误互不相同 + 幂等 + 被拒后状态不变
	b := api.New(1)
	b.Append(rec(10, wal.Begin, "A", ""))
	ok := errors.Is(b.Append(rec(11, wal.Begin, "A", "")), wal.ErrXidOpen) &&
		errors.Is(b.Append(rec(11, wal.Change, "Z", "d")), wal.ErrXidNotOpen) &&
		errors.Is(b.Append(rec(5, wal.Begin, "Z", "")), wal.ErrLSNOrder) &&
		errors.Is(b.Append(rec(11, wal.Begin, "B", "")), slot.ErrTooManyOpen) &&
		errors.Is(b.Confirm(99), slot.ErrNotBoundary) &&
		errors.Is(b.Confirm(1), slot.ErrBackward) &&
		b.Confirm(5) == nil && b.ConfirmedLSN() == 5 && b.RestartLSN() == 5
	check("四类可判定错误+幂等+被拒后状态不变", ok)

	// 大 m：Confirm 与 restart 正确（检查个数不随 m 增长由 slot 包内测试钉住）
	m := 10000
	c := api.New(m + 1)
	c.Append(rec(10, wal.Begin, "T0", ""))
	for i := 0; i < m; i++ {
		c.Append(rec(int64(20+i), wal.Begin, fmt.Sprintf("M%d", i), ""))
	}
	c.Append(rec(int64(20+m), wal.Commit, "T0", ""))
	check("大m检查数不随m增长(见slot测试)", c.Confirm(int64(20+m)) == nil && c.RestartLSN() == 20)

	// 并发：追加/确认/读两个 LSN 单调
	check("并发读到的LSN单调", concurrent())

	if failed {
		fmt.Println("RESULT FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT OK")
}

func concurrent() bool {
	a, n := api.New(100), 200
	var bad, stop int32
	var wg, rd sync.WaitGroup
	wg.Add(2)
	go func() { // 按序追加交错事务
		defer wg.Done()
		for i, lsn := 0, int64(10); i <= n; i, lsn = i+1, lsn+3 {
			if i < n {
				a.Append(rec(lsn+1, wal.Begin, fmt.Sprintf("T%d", i), ""), rec(lsn+2, wal.Change, fmt.Sprintf("T%d", i), "v"))
			}
			if i > 0 {
				a.Append(rec(lsn+3, wal.Commit, fmt.Sprintf("T%d", i-1), ""))
			}
		}
	}()
	go func() { // 按提交顺序逐个确认
		defer wg.Done()
		for seen := 0; seen < n; seen++ {
			var em []slot.Txn
			for em = a.Emitted(); seen >= len(em); em = a.Emitted() {
			}
			if a.Confirm(em[seen].CommitLSN) != nil {
				atomic.StoreInt32(&bad, 1)
				return
			}
		}
	}()
	for k := 0; k < 4; k++ { // 读者：先 r 后 c，读到因果一致的一对
		rd.Add(1)
		go func() {
			defer rd.Done()
			lc, lr, ok := int64(0), int64(0), true
			for atomic.LoadInt32(&stop) == 0 {
				r, c := a.RestartLSN(), a.ConfirmedLSN()
				ok = ok && c >= lc && r >= lr && r <= c
				lc, lr = c, r
			}
			if !ok {
				atomic.StoreInt32(&bad, 1)
			}
		}()
	}
	wg.Wait()
	atomic.StoreInt32(&stop, 1)
	rd.Wait()
	return atomic.LoadInt32(&bad) == 0
}
