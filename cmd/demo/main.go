// demo：事务发件箱中继的端到端演示，逐条打印 OK/FAIL，全部通过退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/obx"
)

var failed atomic.Bool

func rep(name string, ok bool) {
	s := "OK"
	if !ok {
		s = "FAIL"
		failed.Store(true)
	}
	fmt.Println(name, s)
}

// run 依次执行脚本：W 写(payload=p)、C 提交、A 中止、R0/R1 中继(1=崩溃)；
// 前缀 ! 表示期望该操作被拒。返回各次 R 的投递序列。
func run(a *api.Service, ops ...string) [][]int {
	var ds [][]int
	for _, op := range ops {
		wantErr := op[0] == '!'
		if wantErr {
			op = op[1:]
		}
		var err error
		switch op[0] {
		case 'W':
			err = a.Write(op[1:], "p")
		case 'C':
			err = a.Commit(op[1:])
		case 'A':
			err = a.Abort(op[1:])
		case 'R':
			ds = append(ds, a.Relay(op[1] == '1'))
		}
		if (err != nil) != wantErr {
			failed.Store(true)
		}
	}
	return ds
}

func main() {
	a := api.New(16)
	run(a, "WT1", "WT2", "CT2")
	d4 := run(a, "R0")[0]
	run(a, "WT3", "WT1", "CT3", "CT1")
	d9 := run(a, "R1")[0]
	ap9, du9 := a.Applied(), a.Dups()
	d10 := run(a, "R0")[0]
	good := slices.Equal(d4, []int{2}) && slices.Equal(d9, []int{3, 1, 4}) && slices.Equal(d10, []int{4}) &&
		slices.Equal(ap9, []int{2, 3, 1, 4}) && du9 == 0 && slices.Equal(a.Applied(), ap9) && a.Dups() == 1
	fmt.Printf("ten-step d4=%v d9=%v d10=%v ap9=%v dup9=%d ap10=%v dup10=%d ", d4, d9, d10, ap9, du9, a.Applied(), a.Dups())
	rep("", good)

	b := api.New(8)
	ab := run(b, "Wdead", "Wlive", "Adead", "Clive", "R0")
	rep("abort-not-delivered", slices.Equal(ab[0], []int{2}) && slices.Equal(b.Applied(), []int{2}) && b.Dups() == 0)

	rep("naive-match+selfcheck", api.New(8).SelfCheck() == nil)

	c := api.New(8)
	mk := run(c, "Wt", "Wt", "Ct", "R1", "R0")
	rep("mark-implies-deliver", slices.Equal(mk[0], []int{1, 2}) && slices.Equal(mk[1], []int{2}) && c.Dups() == 1)

	e := api.New(1)
	run(e, "Wt", "Ct")
	e1, e2, e3 := e.Write("t", "p"), e.Commit("ghost"), e.Write("u", "")
	run(e, "Wu")
	e4 := e.Commit("u")
	diff := obx.ErrTxUnavailable != obx.ErrEmptyPayload && obx.ErrEmptyPayload != obx.ErrBacklog && obx.ErrTxUnavailable != obx.ErrBacklog
	rep("three-error-kinds", diff && errors.Is(e1, obx.ErrTxUnavailable) && errors.Is(e2, obx.ErrTxUnavailable) &&
		errors.Is(e3, obx.ErrEmptyPayload) && errors.Is(e4, obx.ErrBacklog))

	s := api.New(2)
	run(s, "Wt", "Wt", "Ct", "!Wt", "!Cghost", "Wu", "!Cu", "Wu", "!Cu")
	r := run(s, "R0", "Cu", "R0")
	rep("reject-keeps-state", slices.Equal(r[0], []int{1, 2}) && slices.Equal(r[1], []int{3, 4}) &&
		slices.Equal(s.Applied(), []int{1, 2, 3, 4}) && s.Dups() == 0)

	scan := true // 检查条数的严格上界由 obx 白盒测试钉住；这里验证功能正确
	for _, m := range []int{100, 1000, 10000} {
		g := api.New(m + 1)
		ops := make([]string, m, m+5)
		for i := range ops {
			ops[i] = "Wbulk"
		}
		d := run(g, append(ops, "Cbulk", "R0", "Wnew", "Cnew", "R0")...)
		scan = scan && slices.Equal(d[1], []int{m + 1}) && len(g.Applied()) == m+1
	}
	rep("scan-free-take(m<=10000)", scan)

	rep("concurrent", concurrentCheck())
	if failed.Load() {
		os.Exit(1)
	}
}

// 8 个 goroutine 各写并提交 4 个事务（每个 3 条），另一个 goroutine 反复
// Relay(true)；全部结束后补一次 Relay(false)，已应用必须是 1..96 的排列，
// 且同事务 3 条连续升序（(csn,id) 序的可观测后果）。
func concurrentCheck() bool {
	a := api.New(1 << 20)
	var stop atomic.Bool
	var rwg, wg sync.WaitGroup
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		for !stop.Load() {
			a.Relay(true)
		}
	}()
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for t := 0; t < 4; t++ {
				x := fmt.Sprintf("g%dt%d", g, t)
				run(a, "W"+x, "W"+x, "W"+x, "C"+x)
			}
		}(g)
	}
	wg.Wait()
	stop.Store(true)
	rwg.Wait()
	a.Relay(false)
	ap, seen, ok := a.Applied(), map[int]bool{}, true
	for i, id := range ap {
		if id < 1 || id > 96 || seen[id] || (i%3 == 2 && !(ap[i-2] < ap[i-1] && ap[i-1] < id)) {
			ok = false
		}
		seen[id] = true
	}
	return ok && len(ap) == 96
}
