// 分层时间轮调度器演示：逐条演练关键性质并打印 OK/FAIL。
package main

import (
	"fmt"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"unsafe"

	"ontology/scheduler"
	"ontology/timer"
)

var failed bool

func report(name string, ok bool) {
	mark := "OK  "
	if !ok {
		mark = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", mark, name)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// touched 通过反射读取非导出计数器（仅演示用，公开接口不含计数器）。
func touched(s *scheduler.Scheduler) (int64, int64) {
	read := func(name string) int64 {
		f := reflect.ValueOf(s).Elem().FieldByName(name)
		return reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Int()
	}
	return read("touchedSlots"), read("touchedTimers")
}

func main() {
	// 1. 延迟 0：注册后不触发，首次 Advance 触发。
	s, _ := scheduler.New(scheduler.Config{})
	fired := 0
	_, err := s.Add(0, func() { fired++ })
	must(err)
	ok0 := fired == 0
	must(s.Advance(1))
	report("延迟0在首次Advance触发", ok0 && fired == 1)

	// 2. 不早触发：d=5，推进 4 不触发，第 5 tick 触发。
	s, _ = scheduler.New(scheduler.Config{})
	fired = 0
	_, err = s.Add(5, func() { fired++ })
	must(err)
	must(s.Advance(4))
	ok2 := fired == 0
	must(s.Advance(1))
	report("不早触发且到期必触发", ok2 && fired == 1)

	// 3. Advance(100) 与一百次 Advance(1) 序列相同。
	seqs := make([][]int, 2)
	for mode := 0; mode < 2; mode++ {
		sc, _ := scheduler.New(scheduler.Config{})
		var mu sync.Mutex
		for i, d := range []int64{0, 1, 63, 64, 99, 100, 37} {
			id := i
			_, err := sc.Add(d, func() { mu.Lock(); seqs[mode] = append(seqs[mode], id); mu.Unlock() })
			must(err)
		}
		if mode == 0 {
			must(sc.Advance(100))
		} else {
			for i := 0; i < 100; i++ {
				must(sc.Advance(1))
			}
		}
	}
	report("Advance(100)==100xAdvance(1)", reflect.DeepEqual(seqs[0], seqs[1]) && len(seqs[0]) == 7)

	// 4. 同 tick 多个到期：顺序 = 注册先后，与层级无关。
	s, _ = scheduler.New(scheduler.Config{})
	var order []int
	for i, d := range []int64{64, 5, 64, 5} {
		id := i
		_, err := s.Add(d, func() { order = append(order, id) })
		must(err)
	}
	must(s.Advance(64))
	report("同tick顺序=注册先后", reflect.DeepEqual(order, []int{1, 3, 0, 2}))

	// 5. 已取出待触发时被取消：不触发。
	s, _ = scheduler.New(scheduler.Config{})
	var b *timer.Timer
	firedB := false
	_, err = s.Add(5, func() { must(s.Cancel(b)) })
	must(err)
	b, err = s.Add(5, func() { firedB = true })
	must(err)
	must(s.Advance(5))
	report("待触发时被取消则不触发", !firedB)

	// 6. 句柄幂等：三类结果互不相同。
	s, _ = scheduler.New(scheduler.Config{})
	tm, _ := s.Add(1, nil)
	must(s.Cancel(tm))
	errCancel := s.Cancel(tm)
	tf, _ := s.Add(1, nil)
	must(s.Advance(1))
	errFired := s.Cancel(tf)
	errReset := s.Reset(tm, 5)
	report("句柄幂等结果互不相同",
		errCancel == scheduler.ErrAlreadyCancelled &&
			errFired == scheduler.ErrAlreadyFired &&
			errReset == scheduler.ErrAlreadyCancelled &&
			s.Reset(tf, 5) == scheduler.ErrAlreadyFired)

	// 7. 三类超限被拒，且拒绝后仍能正常工作。
	s, _ = scheduler.New(scheduler.Config{MaxAdvance: 10, MaxTimers: 1, MaxDelay: 100})
	keep, _ := s.Add(1, nil)
	_, errTimers := s.Add(1, nil)
	errAdv := s.Advance(11)
	_, errDelay := s.Add(101, nil)
	must(s.Cancel(keep))
	ok7 := false
	if _, err := s.Add(1, func() { ok7 = true }); err == nil {
		must(s.Advance(1))
	}
	report("三类超限被拒且随后正常",
		errTimers == scheduler.ErrTooManyTimers &&
			errAdv == scheduler.ErrAdvanceTooLarge &&
			errDelay == scheduler.ErrDelayTooLarge && ok7)

	// 8. 并发 Add/Cancel/Advance 后自检通过。
	s, _ = scheduler.New(scheduler.Config{MaxTimers: 1 << 20})
	var stop atomic.Bool
	var advWg, workerWg sync.WaitGroup
	advWg.Add(1)
	go func() {
		defer advWg.Done()
		for !stop.Load() {
			must(s.Advance(1))
		}
	}()
	for w := 0; w < 4; w++ {
		workerWg.Add(1)
		go func(w int) {
			defer workerWg.Done()
			for j := 0; j < 200; j++ {
				tm, err := s.Add(int64(j%20), nil)
				if err == nil && j%2 == 0 {
					_ = s.Cancel(tm)
				}
			}
		}(w)
	}
	workerWg.Wait()
	stop.Store(true)
	advWg.Wait()
	for s.Pending() > 0 {
		must(s.Advance(1))
	}
	report("并发后自检通过", s.Check() == nil)

	// 9. 两档触碰数对照：Advance(1) 的触碰数不随 N 增长。
	var counts [2][2]int64
	for i, n := range []int{1000, 100000} {
		sc, _ := scheduler.New(scheduler.Config{MaxTimers: 200000})
		for j := 0; j < n; j++ {
			_, err := sc.Add(1000000, nil)
			must(err)
		}
		must(sc.Advance(1))
		counts[i][0], counts[i][1] = touched(sc)
	}
	fmt.Printf("触碰数 N=1000: %d槽/%d定时器, N=100000: %d槽/%d定时器\n",
		counts[0][0], counts[0][1], counts[1][0], counts[1][1])
	report("触碰数不随N增长", counts[1] == counts[0])

	if failed {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS")
}
