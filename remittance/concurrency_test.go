package remittance

import (
	"fmt"
	"sync"
	"testing"
)

// 并发调用：结果必须等价于某个串行顺序，限额永不被突破。
// 多 goroutine 用单调时间戳依次提交（借助通道发号），再与朴素不变量核对。
func TestConcurrentSubmissionsNeverExceedLimits(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 1_000_000_000, ReviewSeconds: 60, ReviewThreshold: 1_000_000})
	const single, daily = 3, 60
	addTestSender(t, e, "s1", single, daily, daily)

	const goroutines = 16
	const perG = 40

	// 预生成报价；每笔报价只用于一次提交，避免“已消耗”造成大量预期拒绝。
	var quoteIDs []int64
	var now int64
	for i := 0; i < goroutines*perG; i++ {
		now++
		id, err := e.ApplyQuote(QuoteRequest{
			Sender: "s1", SourceCCY: "USD", TargetCCY: "CNY",
			Amount: 1, RatePPM: 1000000, Now: now, // target=occ=1
		})
		if err != nil {
			t.Fatalf("quote: %v", err)
		}
		quoteIDs = append(quoteIDs, id)
	}

	tickets := make(chan int64, len(quoteIDs))
	for i := range quoteIDs {
		tickets <- int64(i)
	}
	var wg sync.WaitGroup
	var ok, rejected int64
	var mu sync.Mutex
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range tickets {
				// 全部并发提交携带同一 now：相等不构成回退；
				// 锁将它们串成确定顺序，先接受者占额、后来者按限额拒绝。
				n := now + 1
				key := fmt.Sprintf("key-%d", idx)
				_, err := e.Submit(SubmitRequest{
					Sender: "s1", QuoteID: quoteIDs[idx],
					Payee: "p", IdemKey: key, Now: n,
				})
				mu.Lock()
				switch CodeOf(err) {
				case 0:
					ok++
				case ErrCodeDailyLimitExceeded, ErrCodeAnnualLimitExceeded:
					rejected++
				default:
					t.Errorf("unexpected error under concurrency: %v", err)
				}
				mu.Unlock()
			}
		}()
	}
	close(tickets)
	wg.Wait()

	// 全部提交立即出款（低于审核阈值）。日限额 60 是同一 now 的瓶颈。
	finalNow := now + 1
	u := mustUsage(t, e, "s1", finalNow)
	if u.DayUsed > daily {
		t.Fatalf("daily limit breached under concurrency: used=%d limit=%d",
			u.DayUsed, daily)
	}
	if u.DayUsed != daily {
		t.Fatalf("exactly daily=%d transfers should succeed, got %d (ok=%d rejected=%d)",
			daily, u.DayUsed, ok, rejected)
	}
	if ok != daily || ok+rejected != int64(len(quoteIDs)) {
		t.Fatalf("counts: ok=%d rejected=%d total=%d", ok, rejected, len(quoteIDs))
	}
}

// 同一幂等键并发重复提交：恰好一次“首次”，其余全部原样重放。
func TestConcurrentSameIdemKey(t *testing.T) {
	e := New(Config{QuoteTTLSeconds: 1_000_000_000, ReviewSeconds: 60, ReviewThreshold: 1_000_000})
	addTestSender(t, e, "s1", 100, 100, 100)
	var now int64 = 1
	qid, err := e.ApplyQuote(QuoteRequest{
		Sender: "s1", SourceCCY: "USD", TargetCCY: "CNY",
		Amount: 1, RatePPM: 1000000, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	const n = 32
	start := make(chan struct{})
	turn := make(chan int, 1)
	turn <- 0
	var wg sync.WaitGroup
	results := make(chan SubmitResult, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for {
				v := <-turn
				if v == i {
					break
				}
				turn <- v // 不是自己的轮次，原样放回
			}
			ts := now + 1 + int64(i)
			res, err := e.Submit(SubmitRequest{
				Sender: "s1", QuoteID: qid, Payee: "p",
				IdemKey: "same", Now: ts,
			})
			if err != nil {
				errs <- err
				turn <- (i + 1) % n // 即便失败也放行下一位
				return
			}
			results <- res
			if i+1 < n {
				turn <- i + 1
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("concurrent same-key submit must all succeed (first/replay): %v", err)
	}
	first := 0
	var tid int64
	for r := range results {
		if !r.Replay {
			first++
			tid = r.TransferID
		} else if r.TransferID != tid {
			t.Fatalf("replay returned different transfer id")
		}
	}
	if first != 1 {
		t.Fatalf("want exactly one non-replay submission, got %d", first)
	}
	u := mustUsage(t, e, "s1", now+n+1)
	if u.AnnualUsed != 1 {
		t.Fatalf("same-key concurrency must occupy exactly once, got %d", u.AnnualUsed)
	}
}
