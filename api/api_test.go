package api

import (
	"errors"
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"testing"

	"ontology/lock"
)

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestSentinelErrorsAPI：经公开 API 的三类拒绝互不相同、失败不留痕、被拒后仍可用。
func TestSentinelErrorsAPI(t *testing.T) {
	x := New()
	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"空读者ID", func() error { return x.AcquireRead("") }, ErrEmptyID},
		{"空写者ID", func() error { return x.AcquireWrite("") }, ErrEmptyID},
		{"空释放ID", func() error { return x.ReleaseRead("") }, ErrEmptyID},
		{"释放未持有读锁", func() error { return x.ReleaseRead("ghost") }, ErrNotHeld},
		{"释放未持有写锁", func() error { return x.ReleaseWrite("ghost") }, ErrNotHeld},
		{"重复释放读锁", func() error {
			_ = x.AcquireRead("R")
			_ = x.ReleaseRead("R")
			return x.ReleaseRead("R")
		}, ErrDoubleRelease},
		{"重复释放写锁", func() error {
			_ = x.AcquireWrite("W")
			_ = x.ReleaseWrite("W")
			return x.ReleaseWrite("W")
		}, ErrDoubleRelease},
	}
	distinct := map[error]bool{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := fmt.Sprint(x.Readers(), x.Writer())
			if err := c.run(); !errors.Is(err, c.want) {
				t.Fatalf("err=%v want %v", err, c.want)
			}
			after := fmt.Sprint(x.Readers(), x.Writer())
			if before != after {
				t.Fatal("被拒操作改动了持有者状态")
			}
			distinct[c.want] = true
		})
	}
	if len(distinct) != 3 {
		t.Fatalf("三类哨兵错误未互不相同: %d", len(distinct))
	}
	if err := x.AcquireRead("ok"); err != nil {
		t.Fatalf("拒绝后锁不可用: %v", err)
	}
	if err := x.ReleaseRead("ok"); err != nil {
		t.Fatal(err)
	}
}

// TestFlagDecisionO1API：规模跨 100/1000/10000 档判定检查条目数不随 m 线性增长，不读非导出计数器。
func TestFlagDecisionO1API(t *testing.T) {
	if !lock.FlagDecisionO1() {
		t.Fatal("写者优先判定未做到 O(1)（疑似整表扫描等待队列）")
	}
}

// TestConcurrentAPI：N goroutine 并发 acquire/release，原子快照核验互斥；不用 sleep。
func TestConcurrentAPI(t *testing.T) {
	x := New()
	const N = 8
	var workers, observers sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < N; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			rnd := rand.New(rand.NewSource(int64(i + 1)))
			id := fmt.Sprintf("g%d", i)
			for n := 0; n < 150; n++ {
				if rnd.Intn(2) == 0 {
					if err := x.AcquireWrite(id); err != nil {
						t.Error(err)
						return
					}
					if err := x.ReleaseWrite(id); err != nil {
						t.Error(err)
						return
					}
				} else {
					if err := x.AcquireRead(id); err != nil {
						t.Error(err)
						return
					}
					if err := x.ReleaseRead(id); err != nil {
						t.Error(err)
						return
					}
				}
			}
		}(i)
	}
	observers.Add(2)
	go func() { // 原子快照核验互斥
		defer observers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				rs, w, _ := x.l.View()
				if w != "" && len(rs) != 0 {
					t.Errorf("读写互斥被破坏: writer=%s readers=%v", w, rs)
					return
				}
				runtime.Gosched()
			}
		}
	}()
	go func() { // Readers/Writer/SelfCheck 与业务并发调用应安全
		defer observers.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = x.Readers(), x.Writer()
				if err := New().SelfCheck(); err != nil {
					t.Error(err)
					return
				}
				runtime.Gosched()
			}
		}
	}()
	workers.Wait()
	close(stop)
	observers.Wait()
	if x.Writer() != "" || len(x.Readers()) != 0 {
		t.Fatalf("终态未清空: writer=%q readers=%v", x.Writer(), x.Readers())
	}
}
