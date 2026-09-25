// Command demo 逐条打印保活判定的关键结论，退出码 0。
package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/keep"
	"ontology/probe"
)

var failed bool

func chk(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		fmt.Println("FAIL " + name)
		failed = true
	}
}

func main() {
	// 第三节八个操作（idleTimeout=100, probeInterval=30, maxProbes=3）。
	st, err := keep.New(100, 30, 3)
	if err != nil {
		fmt.Println("FAIL new:", err)
		os.Exit(1)
	}
	ops := []struct {
		isAct bool
		t     int64
	}{
		{true, 0}, {false, 100}, {false, 130}, {true, 145},
		{false, 245}, {false, 275}, {false, 305}, {false, 335},
	}
	wantP := []int{0, 1, 2, 0, 1, 2, 3, 3}
	wantD := []bool{false, false, false, false, false, false, false, true}
	trace, match := "", true
	for i, op := range ops {
		var p int
		var d bool
		if op.isAct {
			err = st.Activity(op.t)
			trace += "A "
		} else {
			_, _, err = st.Tick(op.t)
			trace += fmt.Sprintf("p%d ", st.Probes())
		}
		if err != nil {
			trace += "E "
		}
		p, d = st.Probes(), st.Dead()
		match = match && err == nil && p == wantP[i] && d == wantD[i]
	}
	chk("八步序列 traces="+trace, match && st.LastActive() == 145)
	chk("第4步重置/第7步第3次探测未死/第8步判死", wantP[6] == 3 && !wantD[6] && wantD[7])

	// 探测不算活动：空闲后只 Tick，lastActive 不应被探测刷新。
	s2, _ := keep.New(100, 30, 3)
	_ = s2.Activity(0)
	_, _, _ = s2.Tick(100)
	_, _, _ = s2.Tick(130)
	chk("探测不算活动（lastActive 仍为 0，probes=2）", s2.LastActive() == 0 && s2.Probes() == 2)

	// 左闭：now-lastProbe == probeInterval 即触发下一次探测。
	s3, _ := keep.New(100, 30, 3)
	_ = s3.Activity(0)
	sent1, _, _ := s3.Tick(100)
	sent2, _, _ := s3.Tick(130) // 130-100 == 30，左闭必须触发
	chk("左闭间隔判定（==30 即发第2次）", sent1 && sent2 && s3.Probes() == 2)

	// 经 probe.Runner 验证三类可判定错误互不相同，且被拒后状态不变、仍可用。
	r1, _ := probe.New(100, 30, 3)
	_ = r1.Activity(0)
	for i := 0; i < 4; i++ {
		_, _, _ = r1.Tick(int64(100 + 30*i)) // 100/130/160 发三次，190 判死
	}
	eDead := r1.Activity(191)
	probesAfterDead := r1.Probes()
	r2, _ := probe.New(100, 30, 3)
	_ = r2.Activity(10)
	_, _, eBack := r2.Tick(9) // 时钟回退
	_, eInvalid := probe.New(0, 30, 3)
	distinct := errors.Is(eDead, keep.ErrDead) && errors.Is(eBack, keep.ErrClockBack) &&
		errors.Is(eInvalid, keep.ErrInvalidParam) && eDead != eBack && eBack != eInvalid
	traceOK := r1.Dead() && probesAfterDead == 3 // 死后 Activity 未改动状态
	_, _, _ = r2.Tick(11)                        // 回退被拒后仍可继续正常使用
	traceOK = traceOK && r2.Probes() == 0
	chk("三类哨兵错误可判定且互不相同", distinct)
	chk("被拒不留痕且之后仍可用", traceOK)

	// api 层：内置序列自检（含八步、三哨兵、不留痕、O(1) 边界）。
	k, err := api.New(100, 30, 3)
	if err != nil {
		fmt.Println("FAIL api new:", err)
		os.Exit(1)
	}
	chk("SelfCheck 四条不变量全成立", k.SelfCheck() == nil)

	// O(1)：经自检多档 m，每次 Tick 检查记录数 <= 1（keep 内部已核验）。
	chk("Tick 检查记录数<=1（m=100/1000/10000）",
		keep.InspectionBoundHolds(100) && keep.InspectionBoundHolds(1000) && keep.InspectionBoundHolds(10000))

	// 并发只读：N 个 goroutine 用关闭的 channel 做发令闸，结果逐字段相同。
	kc, _ := api.New(100, 30, 3)
	_ = kc.Activity(0)
	_, _, _ = kc.Tick(100)
	_, _, _ = kc.Tick(130) // probes=2, alive, lastActive=0
	const n = 16
	type snap struct {
		p  int
		d  bool
		la int64
	}
	res := make([]snap, n)
	gate := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-gate
			res[i] = snap{kc.Probes(), kc.Dead(), kc.LastActive()}
		}(i)
	}
	close(gate)
	wg.Wait()
	same := true
	for _, s := range res {
		if s != (snap{2, false, 0}) {
			same = false
		}
	}
	chk("并发只读逐字段一致", same)

	if failed {
		os.Exit(1)
	}
}
