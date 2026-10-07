package settlement_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/settlement"
)

// TestConcurrentOperations 并发执行流水录入与结算（go test -race 运行），
// 结束后校验每个商户的资金不变式仍然成立，证明并发效果等价于某个串行顺序。
func TestConcurrentOperations(t *testing.T) {
	const (
		nMerchants = 4
		nWriters   = 8
		txPerW     = 300
	)
	cal := contiguousCal(1, 100000)
	e := settlement.NewEngine(cal)
	cfg := settlement.Config{DelayDays: 2, ReserveBps: 1000, HorizonDays: 3}
	var now atomic.Int64
	now.Store(1)
	for m := 0; m < nMerchants; m++ {
		mustOK(t, e.AddMerchant(now.Load(), fmt.Sprintf("m%d", m), cfg))
	}

	var wg sync.WaitGroup
	// 写流水：now 取自全局原子计数器，发生日取当前 now（恒不晚于 now）。
	for w := 0; w < nWriters; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < txPerW; i++ {
				n := now.Add(1)
				m := fmt.Sprintf("m%d", (w+i)%nMerchants)
				id := fmt.Sprintf("w%d-tx%d", w, i)
				amount := int64((i%7-3)*1000 + w)
				_ = e.PostTransaction(n, m, id, n, amount) // 偶发封账拒绝属预期
			}
		}(w)
	}
	// 每商户一个结算协程，滚动向前追赶。
	for m := 0; m < nMerchants; m++ {
		wg.Add(1)
		go func(m int) {
			defer wg.Done()
			id := fmt.Sprintf("m%d", m)
			for i := 0; i < 50; i++ {
				n := now.Add(1)
				_, _ = e.Settle(n, id, n) // 非营业日/重复结算/时钟回退拒绝属预期
			}
		}(m)
	}
	wg.Wait()

	// 最终追赶到足够远的营业日，再逐商户校验不变式。
	final := now.Add(10)
	for m := 0; m < nMerchants; m++ {
		id := fmt.Sprintf("m%d", m)
		if _, err := e.Settle(final, id, final-5); err != nil {
			t.Fatalf("final settle %q: %v", id, err)
		}
		checkInvariant(t, e, id)
	}
}
