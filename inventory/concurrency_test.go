package inventory

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrent 并发调用各类操作：结果等价于某个串行顺序（由互斥锁保证），
// 运行期不出现数据竞争（配合 -race），且结束后不变量成立。
func TestConcurrent(t *testing.T) {
	s := NewSystem()
	mustOK(t, s.AddWarehouse("W1", 1))
	mustOK(t, s.AddWarehouse("W2", 2))
	mustOK(t, s.AddOnHand("W1", "P", 100000, 0))
	mustOK(t, s.AddOnHand("W2", "P", 100000, 0))
	mustOK(t, s.AddPlannedInbound("W1", "P", "I1", 500, 100000, 0))
	const goroutines = 8
	const opsEach = 300
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g*1000 + 7)))
			for i := 0; i < opsEach; i++ {
				now := int64(i) // 每个协程时间单调；跨协程的回退拒绝是合法结果
				orderID := fmt.Sprintf("g%d-o%d", g, rng.Intn(opsEach/2))
				switch rng.Intn(6) {
				case 0:
					s.CommitOrder(orderID, []OrderLine{{Product: "P", Qty: 1 + rng.Int63n(4)}},
						now, rng.Intn(2) == 0, 100000, 1+rng.Intn(2))
				case 1:
					s.ATP("W1", "P", now)
				case 2:
					s.ConfirmOutbound(orderID, now)
				case 3:
					s.ReleaseReservation(orderID, now)
				case 4:
					s.ReservationDetails(orderID)
				case 5:
					s.AddOnHand("W2", "P", 1, now)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := s.Validate(); err != nil {
		t.Fatalf("并发结束后不变量破坏: %v", err)
	}
}
