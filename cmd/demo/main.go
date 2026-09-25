// demo 变更流偏移归档与保留的演示程序：逐项打印 OK/FAIL，全部通过退出码 0。
package main

import (
	"errors"
	"fmt"
	"maps"
	"os"

	"ontology/api"
	"ontology/arc"
	"ontology/off"
)

var failed bool

func ok(cond bool, msg string) {
	if cond {
		fmt.Println("OK  " + msg)
	} else {
		failed = true
		fmt.Println("FAIL " + msg)
	}
}

func main() {
	// arc 包 + off 包。
	var a arc.Archive
	a.Append(100, 0)
	a.Append(110, 10)
	a.Evict(10) // 严格小于：(110,10) 幸存
	am, aok := a.Max()
	var p off.Partition
	p.Commit(100, 0)
	p.Checkpoint()
	p.Commit(110, 10)
	p.Evict(20, 10)
	ok(aok && am == 110 && p.Recover() == 110, "arc/off: strict< boundary, recover=max(cp,archive)")

	// api：第三节八步序列，逐步核验 C 与此刻 Restart 恢复值。
	v, _ := api.New(10)
	trace := []struct {
		op             func() error
		wantC, wantRec int64
	}{
		{func() error { return v.Commit(0, 100, 0) }, 100, 100},
		{func() error { return v.Checkpoint(0) }, 100, 100},
		{func() error { return v.Commit(0, 110, 10) }, 110, 110},
		{func() error { return v.Commit(0, 120, 15) }, 120, 120},
		{func() error { return v.Evict(0, 20) }, 120, 120},
		{func() error { return v.Commit(0, 130, 30) }, 130, 130},
		{func() error { return v.Evict(0, 40) }, 130, 130},
		{func() error { return v.Evict(0, 45) }, 130, 100},
	}
	all, rec7 := true, int64(0)
	out := ""
	for i, s := range trace {
		if s.op() != nil {
			all = false
		}
		c, _ := v.Committed(0)
		r := v.Restart()[0]
		if c != s.wantC || r != s.wantRec {
			all = false
		}
		if i == 6 {
			rec7 = r
		}
		out += fmt.Sprintf(" %d/%d", c, r)
	}
	ok(all, "8-step trace C/rec:"+out)
	ok(rec7 == 130 && v.Restart()[0] == 100,
		"(甲) step7 rec=130 (cp-only would be 100); (乙) step8 rec=100 (arc-only -inf)")

	// (丙) 保留边界严格小于。
	w, _ := api.New(10)
	w.Commit(0, 100, 0)
	w.Checkpoint(0)
	w.Commit(0, 110, 10)
	w.Evict(0, 20)
	ok(w.Restart()[0] == 110, "(丙) strict< keeps (110,10) rec=110; <= would drop to cp=100")

	// 四类哨兵错误可判定且互不相同。
	_, e2 := api.New(0)
	w.Commit(0, 110, 20) // 先制造合法状态
	errBad := []error{e2, w.Commit(0, 50, 30), w.Commit(0, 200, 5), w.Evict(0, 1)}
	sentinels := []error{api.ErrInvalidRetention, api.ErrNonMonotonic, api.ErrNonMonotonic, api.ErrNowRegression}
	match := true
	for i := range errBad {
		match = match && errors.Is(errBad[i], sentinels[i])
	}
	distinct := true
	s4 := []error{api.ErrInvalidRetention, api.ErrNonMonotonic, api.ErrNowRegression, api.ErrNoCommit}
	for i := range s4 {
		for j := range s4 {
			if i != j && errors.Is(s4[i], s4[j]) {
				distinct = false
			}
		}
	}
	ok(match && distinct && errors.Is(w.Checkpoint(9), api.ErrNoCommit), "4 sentinel errors detectable & distinct")

	// 被拒后状态不变且可继续正常使用。
	c0, _ := v.Committed(0)
	before := v.Restart()
	v.Commit(0, 100, 50)
	v.Commit(0, 200, -1)
	v.Evict(0, 10)
	v.Checkpoint(9)
	c1, _ := v.Committed(0)
	ok(c0 == c1 && maps.Equal(before, v.Restart()) && v.Commit(0, 140, 50) == nil,
		"rejected ops leave no trace; instance still usable")

	// 大 m 驱逐：幸存者精确；检查个数<=k+1 由 arc 白盒测试 TestEvictCheckCount 钉住。
	big, _ := api.New(10)
	for i := 0; i < 10000; i++ {
		big.Commit(0, int64(i+1), int64(i))
	}
	big.Evict(0, 5000) // 阈值 4990，驱逐 4990 条
	ok(big.Restart()[0] == 10000, "evict m=10000 survivors exact; checks<=k+1 via TestEvictCheckCount")

	// 并发只读：N 个 goroutine 结果逐分区相同。
	want := v.Restart()
	start := make(chan struct{})
	res := make(chan bool, 8)
	for g := 0; g < 8; g++ {
		go func() {
			<-start
			good := true
			for n := 0; n < 50; n++ {
				c, _ := v.Committed(0)
				good = good && c == 140 && maps.Equal(v.Restart(), want) && v.SelfCheck() == nil
			}
			res <- good
		}()
	}
	close(start)
	consistent := true
	for g := 0; g < 8; g++ {
		consistent = consistent && <-res
	}
	ok(consistent, "concurrent Committed/Restart/SelfCheck reads consistent")
	ok(v.SelfCheck() == nil, "SelfCheck")

	if failed {
		os.Exit(1)
	}
}
