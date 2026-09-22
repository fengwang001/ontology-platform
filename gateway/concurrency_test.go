package gateway

import (
	"bytes"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// barrier 让执行函数在测试控制下挂起，模拟长耗时写入。
type barrier struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBarrier() *barrier {
	return &barrier{started: make(chan struct{}, 64), release: make(chan struct{})}
}

func (b *barrier) exec(result []byte) ExecFunc {
	return func(body []byte) ([]byte, error) {
		b.started <- struct{}{}
		<-b.release
		return result, nil
	}
}

func (b *barrier) releaseOnce() { b.once.Do(func() { close(b.release) }) }

func newJoinGateway(onJoin func(string)) (*Gateway, *fakeClock) {
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	return New(Config{
		Now:    func() time.Time { return clock.t },
		TTL:    time.Minute,
		OnJoin: onJoin,
	}), clock
}

func TestConcurrentSameBodyConverges(t *testing.T) {
	var joined sync.WaitGroup
	joined.Add(8)
	g, _ := newJoinGateway(func(string) { joined.Done() })
	b := newBarrier()

	go g.Submit("k", []byte("body"), b.exec([]byte("R")))
	<-b.started

	const n = 8
	var wg sync.WaitGroup
	outs := make([]Outcome, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			outs[i] = g.Submit("k", []byte("body"), b.exec([]byte("R")))
		}(i)
	}

	joined.Wait() // 8 个后来者都已进入合流等待
	b.releaseOnce()
	wg.Wait()

	replay := 0
	for i, out := range outs {
		if !out.Replayed || !bytes.Equal(out.Result, []byte("R")) || out.Err != nil {
			t.Fatalf("goroutine %d 未拿到同一回放结果: %+v", i, out)
		}
		replay++
	}
	if replay != n || g.ExecCalls() != 1 {
		t.Fatalf("应有 %d 次回放、执行 1 次，实际 replay=%d exec=%d", n, replay, g.ExecCalls())
	}
}

func TestConflictWhileRunningIsImmediate(t *testing.T) {
	g, _ := newJoinGateway(nil)
	b := newBarrier()
	var conflictExecRan atomic.Bool

	go g.Submit("k", []byte("body-a"), b.exec([]byte("A")))
	<-b.started

	done := make(chan Outcome, 1)
	go func() {
		done <- g.Submit("k", []byte("body-b"), func([]byte) ([]byte, error) {
			conflictExecRan.Store(true)
			return nil, nil
		})
	}()

	out := <-done // 合流等待会挂住；正确实现这里必须立刻返回冲突
	if !IsConflict(out.Err) {
		t.Fatalf("执行中异体必须立刻冲突，实际 %v", out.Err)
	}
	if conflictExecRan.Load() {
		t.Fatal("执行中遇到异体绝不能执行")
	}
	b.releaseOnce()
}

func TestRunningUnaffectedByExpiry(t *testing.T) {
	g, clock := newJoinGateway(nil)
	b := newBarrier()

	finished := make(chan Outcome, 1)
	go func() { finished <- g.Submit("k", []byte("body"), b.exec([]byte("late"))) }()
	<-b.started

	clock.t = clock.t.Add(time.Hour) // 名义上早该过期
	info := g.Lookup("k")
	if !info.Known || info.State.String() != "running" {
		t.Fatalf("执行中的记录不得因过期消失: %+v", info)
	}

	b.releaseOnce()
	out := <-finished
	if out.Err != nil || !bytes.Equal(out.Result, []byte("late")) {
		t.Fatalf("长耗时执行结束后结果应正常保存: %+v", out)
	}
}

func TestNoGlobalLockBlockingOtherKeys(t *testing.T) {
	g, _ := newJoinGateway(nil)
	long := newBarrier()

	go g.Submit("slow", []byte("body"), long.exec([]byte("slow-result")))
	<-long.started

	done := make(chan Outcome, 1)
	go func() {
		done <- g.Submit("fast", []byte("body"), func([]byte) ([]byte, error) {
			return []byte("fast-result"), nil
		})
	}()

	out := <-done // 被全局锁卡住时这里永远不会返回
	if out.Replayed || !bytes.Equal(out.Result, []byte("fast-result")) {
		t.Fatalf("其他键必须能在长耗时执行期间完成: %+v", out)
	}
	long.releaseOnce()
}

func TestDifferentKeysEachExecuteOnce(t *testing.T) {
	g, _ := newJoinGateway(nil)
	var count atomic.Int64
	exec := func(body []byte) ([]byte, error) {
		count.Add(1)
		return append([]byte(nil), body...), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		key := string(rune('a' + i%5))
		go func() { defer wg.Done(); g.Submit(key, []byte(key), exec) }()
	}
	wg.Wait()
	if count.Load() != 5 || g.ExecCalls() != 5 {
		t.Fatalf("5 个不同键应各执行一次，实际 %d", count.Load())
	}
}
