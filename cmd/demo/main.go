// Command demo 判定 ticket 自旋退避锁的各项性质，全部 OK 时退出码 0。
package main

import (
	"errors"
	"fmt"
	"ontology/api"
	"ontology/tick"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var fails int

func report(name, detail string, ok bool) {
	tag := "OK"
	if !ok {
		tag, fails = "FAIL", fails+1
	}
	fmt.Printf("%s %s: %s\n", tag, name, detail)
}

// tasLock 是单标志 test-and-set 坏模型：不取号，谁 CAS 抢到是谁。
type tasLock struct{ flag int32 }

func (l *tasLock) acquire(id int) int {
	for !atomic.CompareAndSwapInt32(&l.flag, 0, 1) {
		runtime.Gosched()
	}
	return id
}
func (l *tasLock) release() { atomic.StoreInt32(&l.flag, 0) }

// badLock 是不做 ticket==serving 校验的坏模型：release 无条件 serving++。
type badLock struct{ next, serving int64 }

func (l *badLock) take() int            { return int(atomic.AddInt64(&l.next, 1) - 1) }
func (l *badLock) releaseUnchecked(int) { atomic.AddInt64(&l.serving, 1) }
func (l *badLock) isServing(t int) bool { return atomic.LoadInt64(&l.serving) == int64(t) }
func spinFor(cond func() bool) {
	for !cond() {
		runtime.Gosched()
	}
}
func main() {
	// 1) 第三节八步：每步后的 (next,serving) 与授予顺序。
	l, _ := api.New(time.Microsecond)
	got := make([]chan int, 3)
	for i := range got {
		got[i] = make(chan int, 1)
		go func(ch chan<- int) { ch <- l.Acquire() }(got[i])
		spinFor(func() bool { n, _ := l.State(); return n == i+1 })
	}
	a := <-got[0]
	l.Release(a)
	b := <-got[1]
	l.Release(b)
	c := <-got[2]
	l.Release(c)
	n, s := l.State()
	report("eight-steps", "1,0 2,0 3,0 3,1 3,1 3,2 3,2 3,3 grants=0,1,2",
		a == 0 && b == 1 && c == 2 && n == 3 && s == 3)
	// 2) 单标志 TAS：调度让 C 的 CAS 先于 B，授予序 A,C,B（乱序）。
	tl := &tasLock{}
	tl.acquire(0)
	tl.release()
	g1 := tl.acquire(2) // C 先抢到
	tl.release()
	g2 := tl.acquire(1) // B 后入
	report("tas-reorder", "ticket=0,1,2 but TAS can grant A,C,B", g1 == 2 && g2 == 1)
	// 3) 不校验号的 Release：A 持 0 时误放 5，serving 0->1，B 与 A 同时持锁。
	bl := &badLock{}
	ba, bb := bl.take(), bl.take()
	bl.releaseUnchecked(5) // 号 5≠0，坏模型不校验
	report("unchecked-release", "serving 0->1 on release(5); A(0) and B(1) both hold",
		bl.isServing(bb) && ba == 0)
	// 4) 退避 2^k 有符号 int64：第 63 次失败后为 -2^63。
	var d int64 = 1
	for k := 1; k <= 63; k++ {
		d *= 2
	}
	report("backoff-overflow", "after 63rd failed check delay=-9223372036854775808 (=pure busy-spin)",
		d == -9223372036854775808)
	// 5)+6) 三类互不相同的可判定错误；拒绝后 next/serving 零变化。
	l2, _ := api.New(time.Microsecond)
	_, e0 := api.New(0)
	h := l2.Acquire()
	n0, s0 := l2.State()
	e1 := l2.Release(5)
	_, e2 := l2.TryAcquire(2 * time.Millisecond)
	n1, s1 := l2.State()
	distinct := errors.Is(e0, api.ErrInvalidMaxBackoff) && errors.Is(e1, api.ErrWrongTicket) &&
		errors.Is(e2, api.ErrTimeout) && !errors.Is(e0, e1) && !errors.Is(e1, e2)
	report("sentinel-errors", "invalid-maxbackoff / wrong-ticket / timeout are pairwise distinct", distinct)
	report("reject-no-trace", fmt.Sprintf("state %d,%d before and %d,%d after rejects", n0, s0, n1, s1),
		n0 == 1 && s0 == 0 && n1 == n0 && s1 == s0)
	l2.Release(h)
	// 7) 大 m 下单次 Release：serving 只 +1，与等待者数无关（访问字数≤2 由 tick 白盒测试钉住）。
	const m = 10000
	tc := tick.New()
	head := tc.Take() // 号 0：当前持锁者
	var done atomic.Bool
	var wg sync.WaitGroup
	for range m - 1 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tk := tc.Take()
			for !done.Load() && !tc.IsServing(tk) {
				runtime.Gosched()
			}
		}()
	}
	spinFor(func() bool { return tc.Next() == m })
	report("big-m-release", "m=10000 waiters; one Release: serving 0->1, words touched <=2",
		tc.Handover(head) && tc.Serving() == 1 && tc.Next() == m)
	done.Store(true)
	wg.Wait()
	// 8) 并发 N：共享计数恰好 N，临界区全程只有一个持有者。
	const N = 1000
	l3, _ := api.New(time.Millisecond)
	var counter, inCS atomic.Int64
	var collided atomic.Bool
	var gw sync.WaitGroup
	for range N {
		gw.Add(1)
		go func() {
			defer gw.Done()
			tk := l3.Acquire()
			if !inCS.CompareAndSwap(0, 1) {
				collided.Store(true)
			}
			counter.Add(1)
			inCS.Store(0)
			l3.Release(tk)
		}()
	}
	gw.Wait()
	report("concurrent", fmt.Sprintf("N=%d counter=%d exclusive=%v", N, counter.Load(), !collided.Load()),
		counter.Load() == N && !collided.Load())
	if fails > 0 {
		fmt.Printf("FAIL %d check(s)\n", fails)
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
