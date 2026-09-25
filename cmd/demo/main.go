package main

import (
	"fmt"
	"os"
	"reflect"
	"sync/atomic"

	"ontology/api"
)

var failed bool

func report(ok bool, msg string) {
	s := "OK "
	if !ok {
		s, failed = "FAIL ", true
	}
	fmt.Println(s + msg)
}

func waitFor(cond func() bool) bool {
	for i := 0; i < 1<<28 && !cond(); i++ {
	}
	return cond()
}

func eightStep(rw *api.RWLock) ([]api.Snapshot, bool) { // 复现第三节八步
	snaps := make([]api.Snapshot, 8)
	do := func(f func(int) error, o, i int) { _ = f(o); snaps[i] = rw.Snapshot() }
	do(rw.AcquireRead, 1, 0) // 1
	do(rw.AcquireRead, 2, 1) // 2
	t3, t4 := make(chan error, 1), make(chan error, 1)
	go func() { t3 <- rw.AcquireWrite(3) }()
	ok := waitFor(func() bool { return rw.Snapshot().WaitingWriters == 1 })
	snaps[2] = rw.Snapshot() // 3
	go func() { t4 <- rw.AcquireRead(4) }()
	ok = ok && waitFor(func() bool { return len(rw.Snapshot().WaitingReaders) == 1 })
	snaps[3] = rw.Snapshot() // 4
	do(rw.ReleaseRead, 1, 4) // 5
	_ = rw.ReleaseRead(2)    // 6 -> T3 获写
	ok = ok && <-t3 == nil
	snaps[5] = rw.Snapshot()
	_ = rw.ReleaseWrite(3) // 7 -> T4 获读
	ok = ok && <-t4 == nil
	snaps[6] = rw.Snapshot()
	do(rw.ReleaseRead, 4, 7) // 8
	return snaps, ok
}

func snap(rs []int, w, ww int, wr []int) api.Snapshot {
	return api.Snapshot{Readers: rs, Writer: w, WaitingWriters: ww, WaitingReaders: wr}
}

func bugModels() (noPref, sticky, deadlock bool) { // 三个错误实现的教学模型
	readers := map[int]bool{1: true, 2: true, 4: true} // 无写者优先：T4 直接获读
	noPref = len(readers) == 3                         // 读者不断则 T3 永远等不到
	writer, waitW := -1, true                          // 写者已释放但标志残留
	sticky = !(writer < 0 && !waitW)                   // T4 获读条件永假
	readers = map[int]bool{1: true}                    // 只剩 T1 持读
	deadlock = len(readers) > 0                        // T1 持读等写：自己的读使条件永假
	return
}

func faultInjection() bool { // 四类哨兵错误互异、被拒零变化、之后仍可用
	rw := api.New()
	s0, e1, e2, e3 := rw.Snapshot(), rw.ReleaseRead(1), rw.ReleaseWrite(1), rw.TryUpgrade(1)
	z1 := reflect.DeepEqual(rw.Snapshot(), s0)
	_ = rw.AcquireRead(1)
	_ = rw.AcquireRead(2)
	s1, e4, e5 := rw.Snapshot(), rw.AcquireRead(1), rw.TryUpgrade(1)
	z2 := reflect.DeepEqual(rw.Snapshot(), s1)
	_, _ = rw.ReleaseRead(1), rw.ReleaseRead(2)
	_ = rw.AcquireWrite(1)
	s2, e6 := rw.Snapshot(), rw.AcquireWrite(1)
	z3 := reflect.DeepEqual(rw.Snapshot(), s2)
	_ = rw.ReleaseWrite(1)
	usable, seen := rw.AcquireRead(9) == nil && len(rw.Snapshot().Readers) == 1, map[error]bool{}
	for _, e := range []error{e1, e2, e3, e4, e5, e6} {
		if e == nil || seen[e] {
			return false
		}
		seen[e] = true
	}
	return z1 && z2 && z3 && usable
}

func concurrencyOK() bool { // 8 reader + 1 writer：值单调不减；有写者则无读者
	rw := api.New()
	var counter, bad, stop int64
	done := make(chan struct{}, 8)
	for g := 0; g < 8; g++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			var last int64
			for atomic.LoadInt64(&stop) == 0 {
				_ = rw.AcquireRead(id)
				v, s := atomic.LoadInt64(&counter), rw.Snapshot()
				_ = rw.ReleaseRead(id)
				if v < last || (s.Writer >= 0 && len(s.Readers) > 0) {
					atomic.StoreInt64(&bad, 1)
					return
				}
				last = v
			}
		}(g)
	}
	for i := 0; i < 500; i++ {
		_ = rw.AcquireWrite(99)
		atomic.AddInt64(&counter, 1)
		s := rw.Snapshot()
		_ = rw.ReleaseWrite(99)
		if s.Writer != 99 || len(s.Readers) != 0 {
			return false
		}
	}
	atomic.StoreInt64(&stop, 1)
	for g := 0; g < 8; g++ {
		<-done
	}
	return bad == 0 && counter == 500
}

func main() {
	rw := api.New()
	snaps, ok := eightStep(rw)
	want := []api.Snapshot{
		snap([]int{1}, -1, 0, nil), snap([]int{1, 2}, -1, 0, nil),
		snap([]int{1, 2}, -1, 1, nil), snap([]int{1, 2}, -1, 1, []int{4}),
		snap([]int{2}, -1, 1, []int{4}), snap(nil, 3, 0, []int{4}),
		snap([]int{4}, -1, 0, nil), snap(nil, -1, 0, nil),
	}
	report(ok && reflect.DeepEqual(snaps, want), "八步四状态与推导表一致")
	report(ok && len(snaps[3].WaitingReaders) == 1, "写者优先下 T4 被阻塞")
	noPref, sticky, deadlock := bugModels()
	report(noPref, "无写者优先时被饿的是 T3")
	_, _ = rw.AcquireRead(1), rw.AcquireRead(2)
	report(rw.TryUpgrade(1) == api.ErrUpgradeConflict, "非唯一读者时升级返回冲突错误")
	report(deadlock, "升级做成阻塞式会永久自旋死锁")
	report(sticky, "等待写者标志不清除会饿死 T4")
	report(faultInjection(), "四类哨兵错误互异且被拒后状态零变化")
	report(api.SelfCheck() == nil, "自检通过：写者优先且大 m 判定字段数 <= 3")
	report(concurrencyOK(), "并发读写 reader 见值单调不减")
	if failed {
		os.Exit(1)
	}
}
