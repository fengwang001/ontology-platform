package sched_test

import (
	"sync"
	"testing"

	"ontology/sched"
)

// 并发混合调用：结果须等价于某串行顺序（-race 下无数据竞争、无 panic）。
func TestConcurrentLinearizable(t *testing.T) {
	s := sched.New(4, 1000, 3, 100000)
	if err := s.Register("d", sched.Params{P: 100, O: 0, W: 100}, 0); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// 每组使用单调不减的时刻，避免与其他组交叉回退。
			base := int64(g * 100000)
			for i := 0; i < 300; i++ {
				now := base + int64(i*3)
				id := "g" + itoa2(g) + "_" + itoa2(i)
				_ = s.Enqueue("d", id, 10, int64(i%4), now+50000, now)
				_, _ = s.Deliver("d", now)
				if i%3 == 0 {
					_ = s.Ack("d", id, now+1)
				}
			}
		}(g)
	}
	wg.Wait()
}

func itoa2(i int) string {
	if i == 0 {
		return "0"
	}
	b := make([]byte, 0, 8)
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
