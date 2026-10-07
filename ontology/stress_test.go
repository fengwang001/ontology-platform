package ontology

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestMultiRoundContentionStress 连续多轮：每轮多个 goroutine 同时争夺，
// 胜者提交/败者不得占位；任意时刻 confirmed+inflight 不得超过容量。
func TestMultiRoundContentionStress(t *testing.T) {
	clk := newFakeClock()
	g := NewGuard(Config{Clock: clk, LeaseTTL: time.Hour})
	const cap = 7
	if err := g.EnsureScope(testScope, cap); err != nil {
		t.Fatal(err)
	}

	const rounds = 25
	const contenders = 32

	var violations atomic.Int64
	var stop atomic.Bool

	// 观察者：持续采样，确认任何瞬间占用都不超过容量。
	observerDone := make(chan struct{})
	go func() {
		defer close(observerDone)
		for !stop.Load() {
			snap, _ := g.Snapshot(testScope)
			if snap.Confirmed+snap.InFlight > cap {
				violations.Add(1)
			}
		}
	}()

	for round := 0; round < rounds; round++ {
		// 读出当前版本作为本轮基线。
		snap, _ := g.Snapshot(testScope)
		free := cap - snap.Confirmed
		if free <= 0 {
			break
		}
		// 多请求基于同一旧版本同时争夺；为模拟"最后一个名额"，
		// 只让第一个获批的请求提交，其余获批者在本 goroutine 中回滚，
		// 从而每轮恰好确认一个关联。
		var admitted atomic.Int64

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < contenders; i++ {
			wg.Add(1)
			id := "round-" + itoa(round) + "-" + itoa(i)
			go func() {
				defer wg.Done()
				<-start
				d := g.Begin(beginReq(id, "emp-"+id, "dept-1", snap.Version))
				if !d.Admitted {
					switch d.Reason {
					case RejectInFlight, RejectBaselineConflict:
					// 均为合法的"未抢到唯一名额"结果。
					default:
						t.Errorf("round %d unexpected reject reason: %v", round, d.Reason)
					}
					return
				}
				if admitted.Add(1) == 1 {
					if !g.Commit(id).OK {
						t.Errorf("round %d winner %s commit failed", round, id)
					}
				} else {
					g.Rollback(id)
				}
			}()
		}
		close(start)
		wg.Wait()

		snap2, _ := g.Snapshot(testScope)
		if snap2.InFlight != 0 {
			t.Fatalf("round %d leaked in-flight=%d", round, snap2.InFlight)
		}
		if snap2.Confirmed > cap {
			t.Fatalf("round %d confirmed %d > cap %d", round, snap2.Confirmed, cap)
		}
	}

	stop.Store(true)
	<-observerDone
	if violations.Load() != 0 {
		t.Fatalf("observed %d capacity violations", violations.Load())
	}

	final, _ := g.Snapshot(testScope)
	if final.Confirmed != cap {
		t.Fatalf("final confirmed = %d, want exactly cap %d", final.Confirmed, cap)
	}
	if final.Version != int64(cap) {
		t.Fatalf("final version = %d, want %d", final.Version, cap)
	}

	// 满额后新请求得到的是 CONFIRMED_FULL，且状态不变。
	d := g.Begin(beginReq("after-full", "emp-after", "dept-1", final.Version))
	if d.Reason != RejectConfirmedFull {
		t.Fatalf("want CONFIRMED_FULL, got %v", d.Reason)
	}
}

// TestHeartbeatConcurrent 心跳与提交/回滚并发时不得出现 map 竞争或堆越界。
func TestHeartbeatConcurrent(t *testing.T) {
	clk := newFakeClock()
	g := NewGuard(Config{Clock: clk, LeaseTTL: 5 * time.Second})
	if err := g.EnsureScope(testScope, 50); err != nil {
		t.Fatal(err)
	}

	const n = 40
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := "hb-" + itoa(i)
		if !g.Begin(beginReq(id, "e-"+id, "dept-1", 0)).Admitted {
			t.Fatalf("admit %s", id)
		}
	}

	for i := 0; i < n; i++ {
		wg.Add(2)
		id := "hb-" + itoa(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				g.Heartbeat(id)
			}
		}()
		go func() {
			defer wg.Done()
			if i%2 == 0 {
				g.Commit(id)
			} else {
				g.Rollback(id)
			}
		}()
	}
	wg.Wait()

	snap, _ := g.Snapshot(testScope)
	if snap.InFlight != 0 {
		t.Fatalf("leaked in-flight: %d", snap.InFlight)
	}
	if snap.Confirmed != n/2 {
		t.Fatalf("confirmed = %d, want %d", snap.Confirmed, n/2)
	}
}
