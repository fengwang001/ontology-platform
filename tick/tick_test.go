package tick

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestCountersSequence 表驱动核验发号顺序、错号拒绝不留痕、serving 只增 1。
func TestCountersSequence(t *testing.T) {
	c := New()
	t0, t1, t2 := c.Take(), c.Take(), c.Take()
	if t0 != 0 || t1 != 1 || t2 != 2 {
		t.Fatalf("Take order = %d,%d,%d, want 0,1,2", t0, t1, t2)
	}
	type step struct {
		name        string
		ticket      int
		wantOK      bool
		wantNext    int
		wantServing int
	}
	steps := []step{
		{"wrong-ticket-5", 5, false, 3, 0}, // 号不符：拒绝且 serving 不动
		{"handover-0", 0, true, 3, 1},
		{"stale-ticket-0", 0, false, 3, 1}, // 旧号重复释放同样被拒
		{"handover-1", 1, true, 3, 2},
		{"handover-2", 2, true, 3, 3},
	}
	for _, s := range steps {
		ok := c.Handover(s.ticket)
		if ok != s.wantOK || c.Next() != s.wantNext || c.Serving() != s.wantServing {
			t.Errorf("%s: ok=%v next=%d serving=%d, want ok=%v next=%d serving=%d",
				s.name, ok, c.Next(), c.Serving(), s.wantOK, s.wantNext, s.wantServing)
		}
	}
}

// TestTryTakeFree 表驱动核验：仅空闲队列可取号，有等待者时失败且零变化。
func TestTryTakeFree(t *testing.T) {
	cases := []struct {
		name     string
		park     bool // 先取一个号不释放，制造等待队列
		wantOK   bool
		wantTick int
		wantNext int
	}{
		{"free", false, true, 0, 1},
		{"busy-retry", true, false, 0, 1},
	}
	for _, tc := range cases {
		c := New()
		if tc.park {
			c.Take() // 号 0 持有不释放
		}
		tk, ok := c.TryTakeFree()
		if ok != tc.wantOK || tk != tc.wantTick || c.Next() != tc.wantNext || c.Serving() != 0 {
			t.Errorf("%s: ok=%v tick=%d next=%d serving=%d", tc.name, ok, tk, c.Next(), c.Serving())
		}
	}
}

// TestHandoverAccessWordsO1：m 个线程各持一号自旋，单次 Release 只访问
// 与 m 无关的常数个状态字（≤2），证明 O(1) 交接、不扫描等待者。
func TestHandoverAccessWordsO1(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(fmt.Sprintf("m=%d", m), func(t *testing.T) {
			c := New()
			head := c.Take() // 号 0：当前持锁者
			var done atomic.Bool
			var wg sync.WaitGroup
			for i := 1; i < m; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					tk := c.Take()
					for !done.Load() && !c.IsServing(tk) {
						runtime.Gosched()
					}
				}()
			}
			for c.Next() != m {
				runtime.Gosched()
			}
			if !c.Handover(head) { // 单次 Release：serving 只 +1
				t.Fatalf("head handover failed")
			}
			if w := c.accessedWords(); w > 2 {
				t.Fatalf("single Release touched %d state words, want <= 2 (independent of m=%d)", w, m)
			}
			if c.Serving() != 1 || c.Next() != m {
				t.Fatalf("after one Release serving=%d next=%d, want 1,%d", c.Serving(), c.Next(), m)
			}
			done.Store(true)
			wg.Wait()
		})
	}
}
