package narledger

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentEquivalentToSerial 多个 goroutine 并发调用时：
//   - 不发生数据竞争（go test -race）；
//   - 所有被接受的操作构成某一合法串行顺序；
//   - 最终账面满足不变量，且与接受操作集按序重放的结果完全一致。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	setup := func() *Ledger {
		l := New()
		for i := 0; i < 4; i++ {
			mustOK(t, l.RegisterGrant(fmt.Sprintf("R%d", i), 0, 1_000_000), "grant")
		}
		mustOK(t, l.Receive(0, "M", "base", 1_000_000, 1_000_000), "receive")
		return l
	}

	const n = 200
	// 每张单据使用严格唯一且与 id 顺序一致的时刻，
	// 保证乱序并发时"时钟回退"拒绝集等价于某个串行前缀，
	// 被接受集合恰好对应投递到系统时的最大时间戳前缀。
	mkDisp := func(i int) genOp {
		return genOp{kind: opDispense, now: int64(i + 1), id: fmt.Sprintf("O%04d", i),
			dept: fmt.Sprintf("dept%03d", i/2), applicant: "doc", drug: "M", qty: 2,
			r1: fmt.Sprintf("R%d", i%4), r2: fmt.Sprintf("R%d", (i+1)%4)}
	}

	// 先并发领用；被拒绝的（时钟回退）记录下来，不算入接受集。
	l := setup()
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := make(map[int]bool)
	var errs []error
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			o := mkDisp(i)
			err := l.Dispense(o.now, o.id, o.dept, o.applicant, o.drug, o.qty, o.r1, o.r2)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				accepted[i] = true
				return
			}
			if codeOf(err) != ErrClockRollback {
				errs = append(errs, err)
			}
		}(i)
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发领用出现非时钟回退错误: %v", errs[0])
	}

	// 并发结清所有已接受单据（now 相同且均合法）。
	for i := range accepted {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := l.Settle(int64(n)+1, fmt.Sprintf("O%04d", i), 1, 1, 0); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(errs) != 0 {
		t.Fatalf("并发结清错误: %v", errs[0])
	}
	mustOK(t, l.CheckInvariant(), "concurrent invariant")

	// 以接受集按 now 升序串行重放到独立账册，最终快照必须一致。
	ref := setup()
	for i := 0; i < n; i++ {
		if !accepted[i] {
			continue
		}
		o := mkDisp(i)
		mustOK(t, ref.Dispense(o.now, o.id, o.dept, o.applicant, o.drug, o.qty, o.r1, o.r2), "ref disp")
	}
	for i := range accepted {
		mustOK(t, ref.Settle(int64(n)+1, fmt.Sprintf("O%04d", i), 1, 1, 0), "ref settle")
	}
	if d := EqualSnap(ref.Snapshot(), l.Snapshot()); d != "" {
		t.Fatalf("并发结果不等价于接受集串行重放: %s", d)
	}
}
