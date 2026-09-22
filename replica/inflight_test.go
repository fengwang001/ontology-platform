package replica

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/bus"
	"ontology/version"
)

// 规则 4：同键并发读合并成一次回源，全部拿到同一结果。
func TestConcurrentSameKeySingleFlight(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return "v", 1, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 100, Loader: loader, Clock: clock.Clock()})

	const readers = 8
	var wg sync.WaitGroup
	vals := make([]string, readers)
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, err := r.Get(context.Background(), "k")
			if err != nil {
				t.Error(err)
				return
			}
			vals[i] = v
		}(i)
	}
	<-started
	for r.InFlightWaiters("k") < readers-1 {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	for i, v := range vals {
		if v != "v" {
			t.Fatalf("reader %d got %q", i, v)
		}
	}
	if got := r.Stats().LoaderCalls; got != 1 {
		t.Fatalf("loader calls=%d want 1 (singleflight)", got)
	}
}

// 规则 4：慢键不阻塞其他键。
func TestSlowKeyDoesNotBlockOthers(t *testing.T) {
	slowStarted := make(chan struct{})
	slowRelease := make(chan struct{})
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if key == "slow" {
			close(slowStarted)
			<-slowRelease
		}
		return "v-" + key, 1, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 100, Loader: loader, Clock: clock.Clock()})
	go r.Get(context.Background(), "slow")
	<-slowStarted
	v, _, err := r.Get(context.Background(), "fast")
	if err != nil || v != "v-fast" {
		t.Fatalf("fast key blocked by slow key: %q, %v", v, err)
	}
	close(slowRelease)
}

// 规则 6：回源进行中收到更高版本失效通知，旧结果必须被丢弃。
func TestInvalidateDuringFlightDiscardsStaleResult(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return "old", 1, true, nil // 回源返回的是旧版本数据
		}
		return "new", 2, true, nil
	}
	clock := NewManualClock()
	r := New(Config{TTL: 100, Loader: loader, Clock: clock.Clock()})
	b := bus.New(0)
	r.Attach(b)

	got := make(chan string, 1)
	go func() {
		v, _, err := r.Get(context.Background(), "k")
		if err != nil {
			t.Error(err)
		}
		got <- v
	}()
	<-started // 回源进行中
	if err := b.Publish(bus.Notification{Key: "k", Version: 2}); err != nil {
		t.Fatal(err)
	}
	b.Flush() // 注入更高版本失效通知
	close(release)
	if v := <-got; v != "new" {
		t.Fatalf("reader got %q, stale result must be discarded", v)
	}
	info := r.Inspect("k")
	if info.Version != 2 {
		t.Fatalf("cache holds version %v, stale v1 must not be written", info.Version)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls=%d want 2 (discard triggers refetch)", got)
	}
}

// 规则 12：并发读 + 并发投递 + 并发推进时钟下，规则 6 仍成立。
func TestInvalidateDuringFlightUnderConcurrency(t *testing.T) {
	for trial := 0; trial < 50; trial++ {
		var calls atomic.Int64
		gate := make(chan struct{})
		loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
			n := calls.Add(1)
			<-gate // 每次回源都等闸门，放大竞态窗口
			if n == 1 {
				return "old", 1, true, nil
			}
			return "new", 2, true, nil
		}
		clock := NewManualClock()
		r := New(Config{TTL: 1 << 40, Loader: loader, Clock: clock.Clock()})
		b := bus.New(0)
		r.Attach(b)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			r.Get(context.Background(), "k")
		}()
		go func() {
			defer wg.Done()
			b.Publish(bus.Notification{Key: "k", Version: 2})
			b.Flush()
		}()
		close(gate)
		wg.Wait()
		if info := r.Inspect("k"); info.Exists && info.State.String() == "Valid" && info.Version == 1 {
			t.Fatalf("trial %d: stale v1 visible in cache", trial)
		}
	}
}
