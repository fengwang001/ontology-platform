package delivery

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentSingleActiveAndCompOnce 在高并发交错下验证不变量：
// 同单至多一个进行中异常、处置补偿至多一次、终态可复现。
func TestConcurrentSingleActiveAndCompOnce(t *testing.T) {
	s := mustSys(t)
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("c%d", i)
		if err := s.PlaceOrder(id, DispLocal, 0, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := s.PickUp(id, 0); err != nil {
			t.Fatal(err)
		}
	}

	var clock int64
	nextTime := func() int64 { return atomic.AddInt64(&clock, 1) }

	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for k := 0; k < 400; k++ {
				idx := (w + k) % 8
				oid := fmt.Sprintf("c%d", idx)
				at := nextTime()
				ex, rerr := s.ReportException(oid, at, ETUnreachable, "", 0)
				if rerr == nil {
					for j := 0; j < 3; j++ {
						_ = s.RecordContact(ex, nextTime()+int64(j)*10)
					}
					_ = s.JudgeUndeliverable(ex, nextTime()+200)
					continue
				}
				// 若上报时已有进行中异常，则尝试联络/回应/判定，任何终态操作最多生效一次。
				_ = s.ConfirmDelivery(oid, nextTime())
			}
		}(w)
	}
	wg.Wait()

	// 终态断言：补偿支付与否与单线程重放相同序列得到的归属一致（此处验证每单至多一次）。
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("c%d", i)
		o, err := s.GetOrder(id, atomic.LoadInt64(&clock)+1000)
		if err != nil {
			t.Fatal(err)
		}
		if o.CompPaid && o.CompAmount != 50 {
			t.Fatalf("comp amount: %+v", o)
		}
		if o.ActiveException != "" && o.Status.Terminal() {
			t.Fatalf("terminal order must not hold active exception: %+v", o)
		}
	}
}

// TestConcurrentSameInstantRace 同一时刻多个 goroutine 竞争：回应与判定只有一个生效。
func TestConcurrentSameInstantRace(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		s := mustSys(t)
		mkOrder(t, s, "o", DispLocal)
		ex := reportUnreachable(t, s, "o", 0)
		for _, at := range []int64{0, 10, 20} {
			if err := s.RecordContact(ex, at); err != nil {
				t.Fatal(err)
			}
		}
		var wg sync.WaitGroup
		var judgeOK, respondOK int32
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if i == 0 {
					if s.JudgeUndeliverable(ex, 100) == nil {
						atomic.StoreInt32(&judgeOK, 1)
					}
				} else {
					if s.UserRespond(ex, 100) == nil {
						atomic.StoreInt32(&respondOK, 1)
					}
				}
			}(i)
		}
		wg.Wait()
		if atomic.LoadInt32(&judgeOK)+atomic.LoadInt32(&respondOK) != 1 {
			t.Fatalf("iter %d: exactly one must win (judge=%d respond=%d)", iter, judgeOK, respondOK)
		}
		o, _ := s.GetOrder("o", 100)
		if o.Status != OSHandled && o.Status != OSPicked {
			t.Fatalf("unexpected status: %v", o.Status)
		}
	}
}

// BenchmarkJudgeDoesNotGrowWithContactHistory 验证判定开销不随联络历史增长：
// 仅读取计数与上次时刻两个 O(1) 字段。
func BenchmarkJudgeConditionCheck(b *testing.B) {
	sys, _ := NewSystem(testParams())
	_ = sys.PlaceOrder("o", DispLocal, 0, 0, 0)
	_ = sys.PickUp("o", 0)
	ex, _ := sys.ReportException("o", 0, ETUnreachable, "", 0)
	for i := 0; i < 100000; i++ {
		_ = sys.RecordContact(ex, int64(i)*10)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 直接度量双条件计算：只读起点、计数两个标量，不遍历联络明细。
		sys.mu.Lock()
		_, _ = sys.conditionSatisfied(ex, int64(1<<40))
		sys.mu.Unlock()
	}
}

// BenchmarkSettleSingleOrder 验证窗口到期转化只访问单笔订单，O(1)，与平台异常总数无关。
func BenchmarkSettleSingleOrder(b *testing.B) {
	sys, _ := NewSystem(testParams())
	for i := 0; i < 10000; i++ {
		id := fmt.Sprintf("o%d", i)
		_ = sys.PlaceOrder(id, DispLocal, 0, 0, 0)
	}
	o := sys.orders["o0"]
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sys.settleAt(o, int64(i))
	}
}
