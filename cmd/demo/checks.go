package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"

	"ontology/bus"
	"ontology/entry"
	"ontology/replica"
	"ontology/version"
)

func checkSingleflight() bool {
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
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: loader, Clock: replica.NewManualClock().Clock()})
	const readers = 8
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Get(context.Background(), "k")
		}()
	}
	<-started
	for r.InFlightWaiters("k") < readers-1 {
		runtime.Gosched()
	}
	close(release)
	wg.Wait()
	return r.Stats().LoaderCalls == 1
}

func checkSlowKey() bool {
	started := make(chan struct{})
	release := make(chan struct{})
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if key == "slow" {
			close(started)
			<-release
		}
		return "v", 1, true, nil
	}
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: loader, Clock: replica.NewManualClock().Clock()})
	go r.Get(context.Background(), "slow")
	<-started
	fastDone := make(chan struct{})
	go func() {
		defer close(fastDone)
		r.Get(context.Background(), "fast")
	}()
	<-fastDone // 慢键在途时快键必须完成
	close(release)
	return true
}

func checkFailure() bool {
	boom := errors.New("boom")
	var calls atomic.Int64
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if calls.Add(1) == 1 {
			return "", 0, false, boom
		}
		return "v", 1, true, nil
	}
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: loader, Clock: replica.NewManualClock().Clock()})
	if _, _, err := r.Get(context.Background(), "k"); !errors.Is(err, boom) {
		return false
	}
	if info := r.Inspect("k"); info.State != entry.Hole || info.Version != 0 {
		return false // 失败不得写入缓存
	}
	v, _, err := r.Get(context.Background(), "k")
	return err == nil && v == "v" && calls.Load() == 2
}

func checkInflightInvalidate() bool {
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	loader := func(ctx context.Context, key string) (string, version.Version, bool, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			return "old", 1, true, nil
		}
		return "new", 2, true, nil
	}
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: loader, Clock: replica.NewManualClock().Clock()})
	bp := bus.New(0)
	r.Attach(bp)
	done := make(chan string, 1)
	go func() {
		v, _, _ := r.Get(context.Background(), "k")
		done <- v
	}()
	<-started
	bp.Publish(bus.Notification{Key: "k", Version: 2})
	bp.Flush()
	close(release)
	v := <-done
	return v == "new" && r.Inspect("k").Version == 2
}

func checkHoleVsNotFound() bool {
	be := newBackend()
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: be.load, Clock: replica.NewManualClock().Clock()})
	r.Get(context.Background(), "ghost")
	neg := r.Inspect("ghost")
	hole := r.Inspect("never")
	return neg.Exists && neg.State == entry.Valid && !neg.Found && hole == (replica.KeyInfo{})
}

func checkEightReplicas() bool {
	be := newBackend()
	be.write("k", "v1")
	clock := replica.NewManualClock()
	reps := make([]*replica.Replica, 8)
	for i := range reps {
		reps[i] = replica.New(replica.Config{TTL: 100, Loader: be.load, Clock: clock.Clock()})
		reps[i].Get(context.Background(), "k")
	}
	var ns []bus.Notification
	for i := 2; i <= 7; i++ {
		ns = append(ns, bus.Notification{Key: "k", Version: be.write("k", fmt.Sprintf("v%d", i))})
	}
	for i, r := range reps { // 每副本独立的乱序/重复/丢失方案
		plan := append([]bus.Notification(nil), ns...)
		for a := range plan {
			b := (a*5 + i) % len(plan)
			plan[a], plan[b] = plan[b], plan[a]
		}
		plan = append(plan, plan[i%len(plan)])
		plan = plan[1:]
		bp := bus.New(0)
		r.Attach(bp)
		bp.PublishAll(plan)
		bp.Flush()
	}
	clock.Advance(100)
	for _, r := range reps {
		r.Get(context.Background(), "k")
		if r.Inspect("k").Version != 7 {
			return false
		}
	}
	return true
}
