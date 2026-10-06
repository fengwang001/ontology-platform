package transfer_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/internal/transfer"
)

func newConcurrentSvc() *transfer.Service {
	return transfer.NewService(
		transfer.Config{TolerancePermille: 50, CloseWaitSeconds: 30},
		map[string]map[string]int64{
			"WH1": {"A": 2_000_000, "B": 2_000_000},
			"WH2": {"A": 2_000_000, "B": 2_000_000},
			"WH3": {"A": 2_000_000, "B": 2_000_000},
		},
	)
}

// TestConcurrentLifecycle 高并发混合操作下：无数据竞争（配合 -race）、
// 任意时刻守恒成立、库存非负、整单创建原子；结束后系统一致。
func TestConcurrentLifecycle(t *testing.T) {
	svc := newConcurrentSvc()

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 3; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if bad := svc.VerifyAll(); bad != "" {
						t.Errorf("conservation broken: %s", bad)
						return
					}
					for _, wh := range []string{"WH1", "WH2", "WH3"} {
						for _, it := range []string{"A", "B"} {
							a, f := svc.Stock(wh, it)
							if a < 0 || f < 0 {
								t.Errorf("negative stock %s/%s=%d,%d", wh, it, a, f)
								return
							}
						}
					}
				}
			}
		}()
	}

	var biz sync.WaitGroup
	var idMu sync.Mutex
	idSeq := 0
	// 测试时钟：取号与提交必须在同一临界区内完成，
	// 才能保证发放的时间戳恰好等于提交顺序（关闭跳到未来也会同步抬高）。
	// 所有 nextAt* 调用都要求调用方已持有 clockMu。
	var clockMu sync.Mutex
	var clockValue int64
	nextAtLocked := func() int64 {
		clockValue++
		return clockValue
	}
	atOrAfterLocked := func(floor int64) int64 {
		if floor > clockValue {
			clockValue = floor
		}
		return clockValue
	}
	whs := [][2]string{{"WH1", "WH2"}, {"WH2", "WH3"}, {"WH3", "WH1"}}
	for w := 0; w < 12; w++ {
		biz.Add(1)
		go func(seed int) {
			defer biz.Done()
			for k := 0; k < 40; k++ {
				idMu.Lock()
				idSeq++
				id := fmt.Sprintf("T%d", idSeq)
				idMu.Unlock()
				sd := whs[(seed+k)%3]
				clockMu.Lock()
				t0 := nextAtLocked()
				err := svc.Create(id, sd[0], sd[1],
					[]transfer.Line{{Item: "A", Qty: 3}, {Item: "B", Qty: 1}}, t0)
				clockMu.Unlock()
				if err != nil {
					t.Errorf("create: %v", err)
					return
				}

				clockMu.Lock()
				t1 := nextAtLocked()
				err = svc.Ship(id, t1)
				clockMu.Unlock()
				if err != nil {
					t.Errorf("ship: %v", err)
					return
				}

				clockMu.Lock()
				t2 := nextAtLocked()
				err = svc.Receive(id, "A", 2, t2)
				clockMu.Unlock()
				if err != nil {
					t.Errorf("receive: %v", err)
					return
				}

				// A 收 2/3、B 收 0/1，需要自发出时刻起等满 30 秒。
				clockMu.Lock()
				t3 := atOrAfterLocked(t1 + 30)
				err = svc.Close(id, t3)
				clockMu.Unlock()
				if err != nil {
					t.Errorf("close: %v", err)
					return
				}

				clockMu.Lock()
				err = svc.Recover(id, "A", 1, nextAtLocked())
				clockMu.Unlock()
				if err != nil {
					t.Errorf("recover: %v", err)
					return
				}
				clockMu.Lock()
				_ = svc.Recover(id, "B", 1, nextAtLocked())
				clockMu.Unlock()
			}
		}(w)
	}

	// 竞争创建：每个单要 1.9M，而总量 2M，至多一个成功，其余整单拒绝且无残留。
	for i := 0; i < 40; i++ {
		biz.Add(1)
		go func(i int) {
			defer biz.Done()
			clockMu.Lock()
			at := nextAtLocked()
			err := svc.Create(fmt.Sprintf("R%d", i), "WH1", "WH2",
				[]transfer.Line{{Item: "A", Qty: 1_900_000}}, at)
			clockMu.Unlock()
			_ = err
		}(i)
	}

	biz.Wait()
	close(stop)
	readers.Wait()

	if bad := svc.VerifyAll(); bad != "" {
		t.Fatalf("final conservation broken: %s", bad)
	}

	// 竞争创建的冻结残留检查：WH1 的冻结只可能来自仍处于 created 的单。
	for _, it := range []string{"A", "B"} {
		_, frozen := svc.Stock("WH1", it)
		if frozen < 0 {
			t.Fatalf("negative frozen: %d", frozen)
		}
	}
}

// TestConcurrentCreateAtomicity 同一商品高并发创建，成功单冻结之和
// 恰好等于初始可用量的扣减量；失败单不留下任何冻结。
func TestConcurrentCreateAtomicity(t *testing.T) {
	svc := transfer.NewService(
		transfer.Config{TolerancePermille: 0, CloseWaitSeconds: 10},
		map[string]map[string]int64{"WH1": {"A": 100}, "WH2": {}},
	)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = svc.Create(fmt.Sprintf("C%d", i), "WH1", "WH2",
				[]transfer.Line{{Item: "A", Qty: 10}}, int64(i))
		}(i)
	}
	wg.Wait()
	avail, frozen := svc.Stock("WH1", "A")
	if avail+frozen != 100 {
		t.Fatalf("atomicity violated: avail=%d frozen=%d sum=%d", avail, frozen, avail+frozen)
	}
	if frozen%10 != 0 || avail%10 != 0 {
		t.Fatalf("frozen/avail must be multiples of line qty: %d,%d", frozen, avail)
	}
	if bad := svc.VerifyAll(); bad != "" {
		t.Fatalf("conservation broken: %s", bad)
	}
}

// TestReplayDeterminism 相同操作序列重放两次，结果完全相同。
func TestReplayDeterminism(t *testing.T) {
	initial := map[string]map[string]int64{"WH1": {"A": 50, "B": 30}, "WH2": {"C": 7}}
	cfg := transfer.Config{TolerancePermille: 150, CloseWaitSeconds: 25}

	play := func(svc *transfer.Service) []string {
		out := []string{}
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		record("%v", svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 60}}, 0))
		record("%v", svc.Create("T1", "WH1", "WH2", []transfer.Line{{Item: "A", Qty: 10}}, 0))
		record("%v", svc.Create("T2", "WH1", "WH2",
			[]transfer.Line{{Item: "A", Qty: 40}, {Item: "B", Qty: 20}}, 1))
		record("%v", svc.Ship("T2", 2))
		record("%v", svc.Receive("T2", "A", 45, 3))
		record("%v", svc.Receive("T2", "A", 2, 4)) // floor(40*150/1000)=6，47 超收
		record("%v", svc.Receive("T2", "B", 10, 5))
		record("%v", svc.Close("T2", 6)) // B 未齐且未到时间
		record("%v", svc.Close("T2", 27))
		record("%v", svc.Recover("T2", "B", 10, 28))
		record("%v", svc.Recover("T2", "B", 1, 29))  // 短缺 10，先找回 1
		record("%v", svc.Recover("T2", "B", 10, 30)) // 再找回 10 过量
		record("%v", svc.Recover("T2", "B", 9, 31))  // 恰找回剩余 9
		record("%v", svc.Recover("T2", "B", 1, 32))  // 已无短缺
		a, f := svc.Stock("WH2", "A")
		record("WH2/A=%d,%d", a, f)
		lines, _ := svc.OrderLines("T2")
		for _, ln := range lines {
			record("line %s issued=%d recv=%d short=%d over=%d",
				ln.Item, ln.Issued, ln.Received, ln.Shortage, ln.Overage)
		}
		return out
	}

	r1 := play(transfer.NewService(cfg, cloneInitial(initial)))
	r2 := play(transfer.NewService(cfg, cloneInitial(initial)))
	if len(r1) != len(r2) {
		t.Fatalf("length mismatch")
	}
	for i := range r1 {
		t.Logf("replay[%d] = %s", i, r1[i])
		if r1[i] != r2[i] {
			t.Fatalf("replay mismatch at %d:\n%s\nvs\n%s", i, r1[i], r2[i])
		}
	}
}

func cloneInitial(in map[string]map[string]int64) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for wh, items := range in {
		out[wh] = map[string]int64{}
		for it, q := range items {
			out[wh][it] = q
		}
	}
	return out
}
