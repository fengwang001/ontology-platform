package api

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// refLock：sync.Mutex 保护的朴素 FIFO 队列锁，按到达发号、按序授予。
type refLock struct {
	mu              sync.Mutex
	cond            *sync.Cond
	ticket, serving int
}

func newRef() *refLock { r := &refLock{}; r.cond = sync.NewCond(&r.mu); return r }
func (r *refLock) acquire() (t int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t, r.ticket = r.ticket, r.ticket+1
	for t != r.serving {
		r.cond.Wait()
	}
	return
}
func (r *refLock) release(int) { r.mu.Lock(); r.serving++; r.cond.Broadcast(); r.mu.Unlock() }

// TestReferenceAgreement 钉不变量 1（与朴素参照一致）与 3（FIFO 严格按号）：
// 对同一顺序获取/释放脚本，真实锁与 sync.Mutex FIFO 参照锁逐次授予号相同
// 且恰为 0..n-1；多档规模由循环生成。另验 SelfCheck 内置八步无错。
func TestReferenceAgreement(t *testing.T) {
	for _, n := range []int{1, 2, 5, 16, 64} {
		l, err := New(time.Microsecond)
		if err != nil {
			t.Fatal(err)
		}
		rf := newRef()
		for k := 0; k < n; k++ {
			t1 := l.Acquire()
			t2 := rf.acquire()
			if t1 != k || t2 != k {
				t.Fatalf("n=%d step=%d real=%d ref=%d want %d", n, k, t1, t2, k)
			}
			for range 64 {
				runtime.Gosched()
			}
			if e := l.Release(t1); e != nil {
				t.Fatalf("release %d: %v", t1, e)
			}
			rf.release(t2)
		}
		if x, y := l.State(); x != n || y != n {
			t.Fatalf("n=%d final state=%d,%d", n, x, y)
		}
	}
	if l, e := New(time.Microsecond); e != nil || l.SelfCheck() != nil {
		t.Fatalf("SelfCheck: %v", e)
	}
}

// TestConcurrentMutualExclusion 钉不变量 2：N 个 goroutine 临界区 +1，
// 全程至多一个持有者（inCS CAS），共享计数终值恰好 N；多档、无 sleep。
func TestConcurrentMutualExclusion(t *testing.T) {
	for _, N := range []int{10, 100, 1000} {
		l, _ := New(time.Millisecond)
		var counter, inCS atomic.Int64
		var bad atomic.Bool
		var wg sync.WaitGroup
		for range N {
			wg.Add(1)
			go func() {
				defer wg.Done()
				tk := l.Acquire()
				if !inCS.CompareAndSwap(0, 1) {
					bad.Store(true)
				}
				counter.Add(1)
				for range 200 {
					runtime.Gosched()
				}
				inCS.Store(0)
				if e := l.Release(tk); e != nil {
					t.Errorf("release: %v", e)
				}
			}()
		}
		wg.Wait()
		if bad.Load() || counter.Load() != int64(N) {
			t.Fatalf("N=%d overlap=%v counter=%d want %d", N, bad.Load(), counter.Load(), N)
		}
	}
}

// TestRejectedOpsLeaveNoTrace 钉不变量 4：三类哨兵错误互不相同，
// 任何拒绝后 next/serving 零变化，之后锁仍可正常使用。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, mb := range []time.Duration{0, -1} {
		if l, e := New(mb); !errors.Is(e, ErrInvalidMaxBackoff) || l != nil {
			t.Fatalf("New(%d)=(%v,%v)", mb, l, e)
		}
	}
	l, _ := New(time.Microsecond)
	h := l.Acquire() // 号 0 持锁：next=1 serving=0
	stuck := func(tag string) {
		if x, y := l.State(); x != 1 || y != 0 {
			t.Fatalf("%s left state %d,%d", tag, x, y)
		}
	}
	if e := l.Release(5); !errors.Is(e, ErrWrongTicket) {
		t.Fatalf("Release(5)=%v", e)
	}
	stuck("wrong-ticket")
	if _, e := l.TryAcquire(2 * time.Millisecond); !errors.Is(e, ErrTimeout) {
		t.Fatalf("TryAcquire=%v", e)
	}
	stuck("timeout")
	if errors.Is(ErrInvalidMaxBackoff, ErrWrongTicket) ||
		errors.Is(ErrInvalidMaxBackoff, ErrTimeout) || errors.Is(ErrWrongTicket, ErrTimeout) {
		t.Fatal("sentinel errors not distinct")
	}
	if e := l.Release(h); e != nil {
		t.Fatalf("recover release: %v", e)
	}
	t2, e := l.TryAcquire(time.Millisecond)
	if e != nil || t2 != 1 {
		t.Fatalf("after recovery (%d,%v)", t2, e)
	}
	if e := l.Release(t2); e != nil {
		t.Fatalf("release t2: %v", e)
	}
}
