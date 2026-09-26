// demo 演示 2PC 协调者状态机：六步推导、四条不变量、故障注入与并发。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/coord"
)

var failed bool

func ok(cond bool, format string, a ...any) {
	status := "OK"
	if !cond {
		status, failed = "FAIL", true
	}
	fmt.Printf("%s %s\n", status, fmt.Sprintf(format, a...))
}

func main() {
	// 第三节六步推导（S1–S3 未调用 Decide，决定为未决）
	tx := api.New(3)
	steps := []func() (int, int, coord.Decision){
		func() (int, int, coord.Decision) { tx.Vote(0, true); y, n := tx.Counts(); return y, n, coord.Pending },
		func() (int, int, coord.Decision) { tx.Vote(1, true); y, n := tx.Counts(); return y, n, coord.Pending },
		func() (int, int, coord.Decision) { tx.Vote(2, false); y, n := tx.Counts(); return y, n, coord.Pending },
		func() (int, int, coord.Decision) { d, _ := tx.Decide(); y, n := tx.Counts(); return y, n, d },
	}
	wantY, wantN := []int{1, 2, 2, 2}, []int{0, 0, 1, 1}
	wantD := []coord.Decision{coord.Pending, coord.Pending, coord.Pending, coord.Abort}
	for i, s := range steps {
		y, n, d := s()
		ok(y == wantY[i] && n == wantN[i] && d == wantD[i], "S%d yes=%d no=%d %v", i+1, y, n, d)
	}
	err := tx.Vote(2, true) // S5：重复投票
	d, _ := tx.Decide()
	ok(errors.Is(err, api.ErrDuplicateVote) && d == coord.Abort, "S5 dup rejected, still %v", d)
	tx2 := api.New(3) // S6：新事务，参与者 2 未投
	tx2.Vote(0, true)
	tx2.Vote(1, true)
	ok(tx2.Recover() == coord.Abort, "S6 recover with 1 missing -> Abort")

	// 全体一致提交 + 票不可改
	tx3 := api.New(3)
	for p := 0; p < 3; p++ {
		tx3.Vote(p, true)
	}
	d3, _ := tx3.Decide()
	tx4 := api.New(2)
	tx4.Vote(0, false)
	e := tx4.Vote(0, true)
	tx4.Vote(1, true)
	d4, _ := tx4.Decide()
	ok(d3 == coord.Commit && errors.Is(e, api.ErrDuplicateVote) && d4 == coord.Abort,
		"unanimous Commit; vote immutable")

	// 三类可判定错误互不相同 + 被拒后状态不变
	tx5 := api.New(2)
	tx5.Vote(0, true)
	e1, e2 := tx5.Vote(5, true), tx5.Vote(0, false)
	_, e3 := tx5.Decide()
	y0, n0 := tx5.Counts()
	distinct := errors.Is(e1, api.ErrOutOfRange) && errors.Is(e2, api.ErrDuplicateVote) &&
		errors.Is(e3, api.ErrNotAllVoted) && e1 != e2 && e2 != e3 && e1 != e3
	tx5.Vote(1, true)
	d5, _ := tx5.Decide()
	y1, n1 := tx5.Counts()
	ok(distinct && y0 == 1 && n0 == 0 && y1 == 2 && n1 == 0 && d5 == coord.Commit,
		"3 distinct errors; rejected ops leave no trace")

	// 大 m 下 Decide 不整表扫描（计数断言见 coord 包内测试，此处验证多档规模判定正确）
	bigOK := true
	for _, m := range []int{100, 1000, 10000} {
		t := api.New(m)
		for p := 0; p < m; p++ {
			t.Vote(p, true)
		}
		if dd, _ := t.Decide(); dd != coord.Commit {
			bigOK = false
		}
	}
	ok(bigOK, "m=100..10000 Decide O(1) via counters")

	// 并发投票后决定正确
	txc := api.New(64)
	var wg sync.WaitGroup
	for p := 0; p < 64; p++ {
		wg.Add(1)
		go func(p int) { defer wg.Done(); txc.Vote(p, true) }(p)
	}
	wg.Wait()
	dc, _ := txc.Decide()
	ok(dc == coord.Commit && api.SelfCheck() == nil, "concurrent votes -> Commit; SelfCheck")

	if failed {
		os.Exit(1)
	}
}
