package chargeback_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/chargeback"
)

// TestConcurrentOps 并发调用所有操作：互斥锁保证结果等价于某个串行顺序。
// 验证手段：-race 下无数据竞争，且任意交错后资金守恒始终成立。
func TestConcurrentOps(t *testing.T) {
	e := chargeback.New(testConfig())
	const workers = 8
	const opsPerWorker = 200

	// 预登记交易，避免并发下重复登记噪声。
	for i := 0; i < 20; i++ {
		mustOK(t, e.AddTransaction(i, chargeback.Transaction{
			ID:         fmt.Sprintf("txn-%d", i),
			SettleDay:  i,
			Amount:     1000,
			CardID:     fmt.Sprintf("card-%d", i%4),
			MerchantID: fmt.Sprintf("mch-%d", i%3),
		}))
	}

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerWorker; i++ {
				now := 20 + rnd.Intn(50)
				txnID := fmt.Sprintf("txn-%d", rnd.Intn(20))
				caseID := fmt.Sprintf("case-%d-%d", seed, i)
				switch rnd.Intn(8) {
				case 0:
					_ = e.OpenDispute(now, caseID, txnID, chargeback.Reason(1+rnd.Intn(2)), int64(1+rnd.Intn(500)))
				case 1:
					_ = e.Respond(now, caseID)
				case 2:
					_ = e.Accept(now, caseID)
				case 3:
					_ = e.PreArbitrate(now, caseID)
				case 4:
					_ = e.Rule(now, caseID, chargeback.Outcome(1+rnd.Intn(2)))
				case 5:
					_, _ = e.MerchantBalance(now, fmt.Sprintf("mch-%d", rnd.Intn(3)))
				case 6:
					_, _ = e.DisputableAmount(now, txnID)
				default:
					_, _ = e.PendingHeld(now)
				}
			}
		}(int64(w))
	}
	wg.Wait()

	// 并发结束后账目必须守恒（任何串行顺序都满足守恒）。
	sum, err := e.LedgerSummary(1000)
	mustOK(t, err)
	if !sum.Balanced() {
		t.Fatalf("并发执行后资金不守恒: %+v", sum)
	}
}
