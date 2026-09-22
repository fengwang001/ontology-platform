package main

import (
	"context"
	"errors"
	"fmt"
	"ontology/bus"
	"ontology/replica"
	"ontology/single"
	"ontology/version"
	"sync"
)

func checkCheckedEntries() bool {
	deltas := map[int]uint64{}
	for _, n := range []int{100, 10000} {
		be := newBackend()
		r := replica.New(replica.Config{TTL: 1 << 40, Loader: be.load, Clock: replica.NewManualClock().Clock()})
		for i := 0; i < n; i++ {
			r.Get(context.Background(), fmt.Sprintf("k%d", i))
		}
		before := r.Stats().CheckedEntries
		deliver(r, bus.Notification{Key: "k0", Version: 2})
		deltas[n] = r.Stats().CheckedEntries - before
	}
	return deltas[100] == 1 && deltas[10000] == 1
}

func checkLimits() bool {
	// 总线队列上限：拒绝且队列不变。
	bp := bus.New(1)
	if err := bp.Publish(bus.Notification{Key: "a", Version: 1}); err != nil {
		return false
	}
	if err := bp.Publish(bus.Notification{Key: "b", Version: 1}); !errors.Is(err, bus.ErrQueueFull) {
		return false
	}
	if bp.Pending() != 1 {
		return false
	}
	// 单飞并发上限：拒绝且计数不变。
	release := make(chan struct{})
	started := make(chan struct{})
	started2 := make(chan struct{})
	var once1, once2 sync.Once
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if key == "slow" || key == "pinned" {
			if key == "slow" {
				once1.Do(func() { close(started) })
			} else {
				once2.Do(func() { close(started2) })
			}
			<-release
		}
		return "v", 1, true, nil
	}
	r := replica.New(replica.Config{TTL: 1 << 40, MaxInflight: 1, Loader: loader, Clock: replica.NewManualClock().Clock()})
	go r.Get(context.Background(), "slow")
	<-started
	before := r.Stats()
	if _, _, err := r.Get(context.Background(), "x"); !errors.Is(err, single.ErrTooManyInflight) {
		return false
	}
	if r.Stats() != before {
		return false
	}
	// 条目数上限：唯一条目被回源钉住不可淘汰，新键拒绝且状态不变。
	r2 := replica.New(replica.Config{TTL: 1 << 40, MaxEntries: 1, Loader: loader, Clock: replica.NewManualClock().Clock()})
	go r2.Get(context.Background(), "pinned")
	<-started2
	before2 := r2.Stats()
	if _, _, err := r2.Get(context.Background(), "y"); !errors.Is(err, replica.ErrTooManyEntries) {
		return false
	}
	if r2.Stats() != before2 {
		return false
	}
	close(release)
	// 三类错误彼此可判定。
	return !errors.Is(replica.ErrTooManyEntries, single.ErrTooManyInflight) &&
		!errors.Is(single.ErrTooManyInflight, bus.ErrQueueFull) &&
		!errors.Is(bus.ErrQueueFull, replica.ErrTooManyEntries)
}

func checkInspectStable() bool {
	be := newBackend()
	be.write("k", "v1")
	clock := replica.NewManualClock()
	r := replica.New(replica.Config{TTL: 10, Loader: be.load, Clock: clock.Clock()})
	r.Get(context.Background(), "k")
	if r.Inspect("k") != r.Inspect("k") {
		return false
	}
	clock.Set(10) // 到期后连查仍一致，且查询不触发回源
	if r.Inspect("k") != r.Inspect("k") {
		return false
	}
	return be.loadCalls() == 1 && r.Inspect("unknown") == (replica.KeyInfo{})
}
