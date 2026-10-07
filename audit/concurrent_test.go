package audit

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentNoGapNoDup 覆盖：并发动作下序号连续、无洞、不重复，
// 且最终活动状态等于审计序列重放结果。
func TestConcurrentNoGapNoDup(t *testing.T) {
	tl := &testLogger{}
	defer tl.flush(t)

	store, log, exec, replayer, _ := newSystem(8)
	const typ = "Counter"
	for i := 0; i < 8; i++ {
		mustRegister(t, exec, typ, fmt.Sprintf("i%d", i), "0")
	}

	const n = 200
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	seqCh := make(chan int64, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(k int) {
			defer wg.Done()
			inst := fmt.Sprintf("i%d", k%8)
			rec, err := exec.Execute(Action{
				ActionID: fmt.Sprintf("act-%d", k),
				TypeName: typ,
				Writes:   []Write{{inst, fmt.Sprintf("v%d", k)}},
			}, nil)
			if err != nil {
				errCh <- err
				return
			}
			seqCh <- rec.Seq
		}(i)
	}
	wg.Wait()
	close(errCh)
	close(seqCh)
	for err := range errCh {
		t.Fatal(err)
	}

	firstActionSeq := int64(9) // 8 条并发前的实例注册占 seq 1..8
	seen := make(map[int64]bool, n)
	for s := range seqCh {
		if seen[s] {
			t.Fatalf("duplicate seq %d", s)
		}
		seen[s] = true
	}
	if len(seen) != n {
		t.Fatalf("unique action seqs=%d want %d", len(seen), n)
	}
	for s := firstActionSeq; s < firstActionSeq+int64(n); s++ {
		if !seen[s] {
			t.Fatalf("hole at seq %d", s)
		}
	}
	tl.logf("并发输入: %d 个动作（注册已占 seq 1..8）| 实际输出: 动作序号集合大小=%d，区间 [%d,%d] 连续 | 依据: 无重复无空洞",
		n, len(seen), firstActionSeq, firstActionSeq+int64(n)-1)

	replayed, err := replayer.StateAt(typ, log.LastSeq(typ))
	if err != nil {
		t.Fatal(err)
	}
	assertState(t, replayed, store.Snapshot(typ), "live state equals serial replay")
	if err := log.VerifyChain(typ); err != nil {
		t.Fatalf("chain: %v", err)
	}
}
