package gateway

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSameKey 并发同键同内容提交：恰好一次被受理，其余均为幂等重放，
// 且返回相同的指令 ID（线性一致性检查）。
func TestConcurrentSameKey(t *testing.T) {
	g := newTestGateway(t)
	mustReport(t, g, "v1", awakeReport(1, 0))

	const n = 16
	results := make([]SubmitResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = g.SubmitCommand(submitReq("v1", "same-key", CmdFindCar, 10, 1000))
		}(i)
	}
	wg.Wait()

	accepted := 0
	var cmdID string
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("第 %d 个并发提交出错: %v", i, errs[i])
		}
		if !results[i].Duplicate {
			accepted++
			cmdID = results[i].CommandID
		}
	}
	if accepted != 1 {
		t.Fatalf("恰好一个提交应被受理, got %d", accepted)
	}
	for i := 0; i < n; i++ {
		if results[i].CommandID != cmdID {
			t.Fatalf("所有并发提交应返回同一指令 ID: %s vs %s", results[i].CommandID, cmdID)
		}
	}
	snap, _ := g.VehicleSnapshot("v1")
	if snap.CommandTotal != 1 {
		t.Fatalf("同键并发提交不应重复受理, CommandTotal=%d", snap.CommandTotal)
	}
}

// TestConcurrentMixed 多车辆混合并发操作：不 panic、无数据竞争，
// 且事后不变量成立（在途数与非终结指令数一致、配额不超上限）。
func TestConcurrentMixed(t *testing.T) {
	cfg := testConfig()
	cfg.WakeupQuotaPerDay = 1000 // 放大配额，聚焦并发而非配额
	g, err := NewGateway(cfg)
	if err != nil {
		t.Fatal(err)
	}
	const vehicles = 8
	const workers = 8
	const opsPerWorker = 400

	var clock atomic.Int64
	clock.Store(-1)
	nextTime := func() int64 { return clock.Add(1) }

	// 预注册并上报。
	for v := 0; v < vehicles; v++ {
		vid := fmt.Sprintf("v%d", v)
		mustReport(t, g, vid, awakeReport(1, nextTime()))
	}

	var mu sync.Mutex
	accepted := map[string][]string{} // vid -> cmdIDs
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < opsPerWorker; i++ {
				vid := fmt.Sprintf("v%d", (w+i)%vehicles)
				tm := nextTime()
				switch i % 4 {
				case 0:
					rep := awakeReport(int64(i+2), tm)
					if i%3 == 0 {
						rep.Power = PowerSleep
					}
					_, _ = g.ReportState(vid, rep)
				case 1:
					req := SubmitRequest{
						VehicleID:   vid,
						Submitter:   fmt.Sprintf("s%d", w),
						RequestID:   fmt.Sprintf("req-%d", i),
						Type:        CmdType(i % CmdTypeCount),
						Time:        tm,
						ValiditySec: 100000,
					}
					res, err := g.SubmitCommand(req)
					if err == nil && !res.Duplicate {
						mu.Lock()
						accepted[vid] = append(accepted[vid], res.CommandID)
						mu.Unlock()
					}
				case 2:
					mu.Lock()
					var cmdID string
					if ids := accepted[vid]; len(ids) > 0 {
						cmdID = ids[len(ids)-1]
					}
					mu.Unlock()
					if cmdID != "" {
						_, _ = g.Ack(vid, cmdID, tm, i%2 == 0)
					}
				case 3:
					_, _ = g.VehicleSnapshot(vid)
				}
			}
		}(w)
	}
	wg.Wait()

	// 不变量：每辆车快照的在途数 == 实际查询到的非终结指令数。
	for v := 0; v < vehicles; v++ {
		vid := fmt.Sprintf("v%d", v)
		snap, ok := g.VehicleSnapshot(vid)
		if !ok {
			t.Fatalf("车辆 %s 应已注册", vid)
		}
		inflight := 0
		for _, cmdID := range accepted[vid] {
			view, ok := g.QueryCommand(vid, cmdID)
			if !ok {
				t.Fatalf("已受理指令 %s 应可查询", cmdID)
			}
			if !view.Status.Terminal() {
				inflight++
			}
			if view.LateAcks < 0 {
				t.Fatalf("迟到回执数不应为负: %+v", view)
			}
		}
		if snap.InFlight != inflight {
			t.Fatalf("车辆 %s 在途数不一致: snapshot=%d 实际=%d", vid, snap.InFlight, inflight)
		}
		if snap.WakeUsedToday > cfg.WakeupQuotaPerDay {
			t.Fatalf("车辆 %s 唤醒配额超限: %d > %d", vid, snap.WakeUsedToday, cfg.WakeupQuotaPerDay)
		}
	}
}
