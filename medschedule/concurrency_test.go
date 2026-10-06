package medschedule

import (
	"sync"
	"testing"
)

// 并发下重放同一操作流的不同交错，只断言不发生数据竞争与崩溃；
// 正确性（任意交错等价于某串行顺序）由全局读写锁保证，用 -race 验证。
func TestConcurrentAccess(t *testing.T) {
	s, _ := NewSystem(3)
	must(t, s.RegisterDrug(0, "D", "C", 1))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := "OG" + itoa(g)
			_ = s.CreateOrder(int64(g), Spec{ID: id, Patient: "P", Drug: "D",
				Kind: "interval", FirstTime: int64(g), H: 50 + int64(g%2)})
			for k := int64(0); k < 50; k++ {
				now := int64(g) + k*7
				_ = s.Administer(now, id)
				_ = s.Refuse(now, id)
				_, _ = s.QueryPatient(now, "P", 0, now+100)
			}
		}(g)
	}
	wg.Wait()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
