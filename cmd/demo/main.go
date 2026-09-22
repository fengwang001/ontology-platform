// 多副本缓存失效传播与回源协调器演示：逐项打印 OK/FAIL。
package main

import (
	"context"
	"fmt"
	"os"
	"sync"

	"ontology/bus"
	"ontology/replica"
	"ontology/version"
)

type backend struct {
	mu    sync.Mutex
	ver   version.Version
	vals  map[string]string
	calls int64
}

func newBackend() *backend { return &backend{vals: map[string]string{}} }

func (b *backend) write(key, val string) version.Version {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.ver++
	b.vals[key] = val
	return b.ver
}

func (b *backend) load(ctx context.Context, key string) (string, version.Version, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	v, ok := b.vals[key]
	return v, b.ver, ok, nil
}

func (b *backend) loadCalls() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func deliver(r *replica.Replica, ns ...bus.Notification) {
	bp := bus.New(0)
	r.Attach(bp)
	if err := bp.PublishAll(ns); err != nil {
		panic(err)
	}
	bp.Flush()
}

type check struct {
	name string
	run  func() bool
}

func allChecks() []check {
	return []check{
		{"shuffled delivery converges (100 trials)", checkShuffle},
		{"duplicate notification idempotent", checkDuplicate},
		{"expiry forces refetch at deadline", checkExpiry},
		{"singleflight merges concurrent reads", checkSingleflight},
		{"slow key does not block others", checkSlowKey},
		{"fetch failure not cached, retryable", checkFailure},
		{"invalidate during flight discards stale", checkInflightInvalidate},
		{"hole vs not-found distinguishable", checkHoleVsNotFound},
		{"8 replicas converge", checkEightReplicas},
		{"checked entries independent of N", checkCheckedEntries},
		{"three limits reject without side effects", checkLimits},
		{"inspect is stable and read-only", checkInspectStable},
	}
}

func main() {
	pass := 0
	checks := allChecks()
	for _, c := range checks {
		status := "OK  "
		if c.run() {
			pass++
		} else {
			status = "FAIL"
		}
		fmt.Printf("%s %s\n", status, c.name)
	}
	fmt.Printf("total: %d/%d checks passed\n", pass, len(checks))
	if pass != len(checks) {
		os.Exit(1)
	}
}

func checkShuffle() bool {
	var want [3]replica.KeyInfo
	keys := []string{"a", "b", "c"}
	for trial := 0; trial <= 100; trial++ {
		be := newBackend()
		for _, k := range keys {
			for v := 1; v <= 3; v++ {
				be.write(k, "v")
			}
		}
		clock := replica.NewManualClock()
		r := replica.New(replica.Config{TTL: 1 << 40, Loader: be.load, Clock: clock.Clock()})
		for _, k := range keys {
			r.Get(context.Background(), k)
		}
		var ns []bus.Notification
		for _, k := range keys {
			for v := 1; v <= 6; v++ {
				ns = append(ns, bus.Notification{Key: k, Version: version.Version(v)})
			}
		}
		if trial > 0 { // trial 0 为顺序基准，其余每次不同伪乱序
			for i := range ns {
				j := (i*7 + 3 + trial) % len(ns)
				ns[i], ns[j] = ns[j], ns[i]
			}
		}
		deliver(r, ns...)
		for i, k := range keys {
			got := r.Inspect(k)
			if trial == 0 {
				want[i] = got
			} else if got != want[i] {
				return false
			}
		}
	}
	return true
}

func checkDuplicate() bool {
	be := newBackend()
	be.write("k", "v1")
	r := replica.New(replica.Config{TTL: 1 << 40, Loader: be.load, Clock: replica.NewManualClock().Clock()})
	r.Get(context.Background(), "k")
	n := bus.Notification{Key: "k", Version: 5}
	deliver(r, n, n, n, n)
	return r.Stats().Invalidations == 1 && r.Inspect("k").Invalidations == 1
}

func checkExpiry() bool {
	be := newBackend()
	be.write("k", "v1")
	clock := replica.NewManualClock()
	r := replica.New(replica.Config{TTL: 10, Loader: be.load, Clock: clock.Clock()})
	r.Get(context.Background(), "k")
	clock.Set(10) // 恰好到期（左闭右开）
	r.Get(context.Background(), "k")
	return be.loadCalls() == 2
}
