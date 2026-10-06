package transfer

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentLinearizable 并发调用下系统不出现数据竞争（配合 -race），
// 任意时刻查询读到的都是某个已完成操作之后的一致快照，
// 且最终守恒成立。时钟由全局原子计数器分配，被拒绝的操作不影响正确性。
func TestConcurrentLinearizable(t *testing.T) {
	s, err := NewSystem(Config{OverReceiptTolerancePermille: 100, CloseWaitSeconds: 0},
		map[string]map[string]int64{
			"W1": {"A": 1_000_000, "B": 1_000_000},
			"W2": {},
			"W3": {},
		})
	if err != nil {
		t.Fatal(err)
	}

	var clock atomic.Int64
	stop := make(chan struct{})
	var readers, writers sync.WaitGroup

	// 读者：持续查询库存与单状态，并做守恒核验。
	for g := 0; g < 4; g++ {
		readers.Add(1)
		go func(g int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.Stock("W1", "A")
				s.OrderLines(fmt.Sprintf("T%d", g))
				if ok, entries := s.VerifyConservation(); !ok {
					t.Errorf("conservation violated concurrently: %+v", entries)
					return
				}
			}
		}(g)
	}

	// 写者：并发创建/发出/收货/关闭/找回。
	for g := 0; g < 8; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("T%d-%d", g, i)
				s.CreateOrder(clock.Add(1), id, "W1", "W2", []Line{
					{Product: "A", Qty: 10},
					{Product: "B", Qty: 10},
				})
				s.ShipOrder(clock.Add(1), id)
				s.Receive(clock.Add(1), id, 0, 10)
				s.Receive(clock.Add(1), id, 1, 5)
				s.CloseOrder(clock.Add(1), id)
				s.Recover(clock.Add(1), id, 1, 5)
				s.CancelOrder(clock.Add(1), id) // 已关闭，预期被拒绝
			}
		}(g)
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	if ok, entries := s.VerifyConservation(); !ok {
		t.Fatalf("final conservation violated: %+v", entries)
	}
	snap, _ := s.Stock("W1", "A")
	if snap.Available < 0 || snap.Frozen < 0 {
		t.Fatalf("negative stock: %+v", snap)
	}
}
