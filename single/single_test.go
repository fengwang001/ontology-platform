package single

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ontology/entry"
)

// blockFn 返回一个"回源函数"和 release()：真实调用后阻塞直到 release。
func blockFn(counter *int64, delay chan<- string) (Fetcher, func()) {
	entered := make(chan struct{})
	release := make(chan struct{})
	fn := func(_ context.Context, key string) (entry.Payload, error) {
		atomic.AddInt64(counter, 1)
		if delay != nil {
			delay <- key
		}
		close(entered)
		<-release
		return entry.Payload{Exists: true, Data: []byte("v:" + key)}, nil
	}
	return fn, func() { close(release) }
}

func TestCoalesceSameKey(t *testing.T) {
	var n int64
	g := New(8)
	fn, release := blockFn(&n, nil)
	const waiters = 50
	var wg sync.WaitGroup
	errs := make([]error, waiters)
	vals := make([][]byte, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			p, err := g.Do(context.Background(), "k", fn)
			errs[i] = err
			vals[i] = p.Data
		}(i)
	}
	// 给 goroutine 一点时间全部挂到同一个 call 上
	time.Sleep(20 * time.Millisecond)
	if got := atomic.LoadInt64(&n); got != 1 {
		t.Fatalf("回源函数被调用 %d 次, want 1", got)
	}
	release()
	wg.Wait()
	for i := 0; i < waiters; i++ {
		if errs[i] != nil || string(vals[i]) != "v:k" {
			t.Fatalf("等待者 %d 结果错误: %v %q", i, errs[i], vals[i])
		}
	}
	if g.FetchCount() != 1 {
		t.Fatalf("FetchCount=%d, want 1", g.FetchCount())
	}
}

func TestSlowKeyDoesNotBlockOthers(t *testing.T) {
	var n int64
	g := New(4)
	entered := make(chan string, 2)
	slowFn, releaseSlow := blockFn(&n, entered)
	go func() { _, _ = g.Do(context.Background(), "slow", slowFn) }()
	<-entered // slow 已占一个席位

	done := make(chan []byte, 1)
	go func() {
		p, err := g.Do(context.Background(), "fast", func(context.Context, string) (entry.Payload, error) {
			atomic.AddInt64(&n, 1)
			return entry.Payload{Exists: true, Data: []byte("fast-ok")}, nil
		})
		if err != nil {
			done <- []byte("err:" + err.Error())
			return
		}
		done <- p.Data
	}()
	select {
	case got := <-done:
		if string(got) != "fast-ok" {
			t.Fatalf("快键结果错误: %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("慢键阻塞了其他键的回源")
	}
	releaseSlow()
}

func TestConcurrencyLimitRejected(t *testing.T) {
	g := New(1)
	entered := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_, _ = g.Do(context.Background(), "a", func(context.Context, string) (entry.Payload, error) {
			close(entered)
			<-release
			return entry.Payload{Exists: true}, nil
		})
	}()
	<-entered
	_, err := g.Do(context.Background(), "b", nil)
	if !errors.Is(err, ErrFlightLimit) {
		t.Fatalf("超并发上限必须返回 ErrFlightLimit, got %v", err)
	}
	close(release)
}

func TestFailureRetriesAndPropagates(t *testing.T) {
	var n int64
	boom := errors.New("backend down")
	g := New(2)
	entered := make(chan struct{})
	release := make(chan struct{})
	fn := func(context.Context, string) (entry.Payload, error) {
		atomic.AddInt64(&n, 1)
		close(entered)
		<-release
		return entry.Payload{}, boom
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := g.Do(context.Background(), "k", fn); !errors.Is(err, boom) {
				t.Errorf("等待者必须拿到同一错误, got %v", err)
			}
		}()
	}
	<-entered
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if atomic.LoadInt64(&n) != 1 {
		t.Fatal("同批失败调用必须合并为一次")
	}
	if _, err := g.Do(context.Background(), "k", fn); !errors.Is(err, boom) {
		t.Fatalf("失败不得缓存, 重试错误传递异常: %v", err)
	}
	if atomic.LoadInt64(&n) != 2 {
		t.Fatal("失败后立即重试必须真正再回源一次")
	}
}

func TestCancelOneWaiter(t *testing.T) {
	g := New(2)
	entered := make(chan struct{})
	release := make(chan struct{})
	fn := func(context.Context, string) (entry.Payload, error) {
		close(entered)
		<-release
		return entry.Payload{Exists: true, Data: []byte("ok")}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	resErr := make(chan error, 2)
	go func() { _, err := g.Do(context.Background(), "k", fn); resErr <- err }()
	<-entered
	go func() { _, err := g.Do(ctx, "k", fn); resErr <- err }()
	cancel()
	if err := <-resErr; !errors.Is(err, context.Canceled) {
		t.Fatalf("取消的等待者应得到 ctx 错误, got %v", err)
	}
	close(release)
	if err := <-resErr; err != nil {
		t.Fatalf("其余等待者仍应拿到结果, got %v", err)
	}
}
