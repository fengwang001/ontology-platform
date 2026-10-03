package receipt

import (
	"fmt"
	"sync"
	"testing"
)

// 并发混压：多个 goroutine 同时投递同一组回执、Tick、Status。
// 以“每个 (kind,attempt) 恰好一次非 Duplicate”作为可线性化计数不变量。
func TestConcurrentAccess(t *testing.T) {
	tr := mustNew(t, 3)
	const nMsg, nRcpt, rounds = 6, 4, 40
	for m := 0; m < nMsg; m++ {
		rs := make([]string, nRcpt)
		for i := range rs {
			rs[i] = fmt.Sprintf("r%d", i)
		}
		mustSend(t, tr, fmt.Sprintf("m%d", m), rs, 1000)
	}

	var wg sync.WaitGroup
	type triple struct {
		msg, rcpt string
		key       receiptKey
	}
	counts := make(map[triple]map[ReceiptOutcome]int)
	var cntMu sync.Mutex
	kinds := []string{"sent", "delivered", "read", "soft", "hard"}

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				m := (worker + i) % nMsg
				r := i % nRcpt
				kind := kinds[(worker+i)%len(kinds)]
				attempt := 1 + (i % 8)
				out, err := tr.Receipt([]byte(fmt.Sprintf("m%d", m)), []byte(fmt.Sprintf("r%d", r)),
					kind, attempt, int64(worker*7+i%50))
				if err != nil {
					t.Errorf("receipt error: %v", err)
					return
				}
				cntMu.Lock()
				k := triple{msg: fmt.Sprintf("m%d", m), rcpt: fmt.Sprintf("r%d", r), key: receiptKey{kind: kind, attempt: attempt}}
				if counts[k] == nil {
					counts[k] = map[ReceiptOutcome]int{}
				}
				counts[k][out]++
				cntMu.Unlock()
			}
		}(w)
	}

	// 并发 Tick：时钟必须单调；重复 now 不应再返回已到期名单。
	var tickClock int64
	var tickMu sync.Mutex
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				tickMu.Lock()
				tickClock += 3
				now := tickClock
				if _, err := tr.Tick(now); err != nil {
					tickMu.Unlock()
					t.Errorf("tick: %v", err)
					return
				}
				tickMu.Unlock()
				if _, err := tr.Status([]byte("m0")); err != nil {
					t.Errorf("status: %v", err)
					return
				}
			}
		}()
	}

	wg.Wait()

	// 注意：同一 (kind,attempt) 被多个 worker 对同一 msg/rcpt 重放，
	// 非 Duplicate 结果可以是 Applied/Stale/Ignored，但总数恰为 1
	// （其余全部 Duplicate）。
	totalNonDup, totalDup := 0, 0
	for k, byOutcome := range counts {
		nonDup := byOutcome[Applied] + byOutcome[Stale] + byOutcome[Ignored]
		if nonDup != 1 {
			t.Fatalf("%s/%s %v: non-duplicate outcomes = %d (%v), want exactly 1",
				k.msg, k.rcpt, k.key, nonDup, byOutcome)
		}
		totalNonDup += nonDup
		totalDup += byOutcome[Duplicate]
	}
	t.Logf("concurrent receipts: %d non-duplicate, %d duplicate", totalNonDup, totalDup)

	// 终态不变量：soft/hard 下 r 不再变化；exp 下 r 必须 < 2。
	for m := 0; m < nMsg; m++ {
		st := status(t, tr, fmt.Sprintf("m%d", m))
		for name, r := range st.Rcpts {
			switch r.Fail {
			case FailExpired:
				if r.R >= 2 {
					t.Fatalf("%s/%s exp but r=%d", fmt.Sprintf("m%d", m), name, r.R)
				}
			}
		}
	}
}

// 相同回执序列重放两次，得到完全相同的状态与逐回执结果。
func TestReplayDeterminism(t *testing.T) {
	play := func() ([]ReceiptOutcome, MessageStatus, [][2]string) {
		tr := mustNew(t, 2)
		mustSend(t, tr, "m", []string{"A", "B", "C"}, 50)
		var outs []ReceiptOutcome
		steps := []struct {
			rcpt, kind string
			attempt    int
			ts         int64
		}{
			{"A", "soft", 1, 0},
			{"A", "sent", 2, 1},
			{"A", "soft", 2, 2},
			{"A", "soft", 3, 3},
			{"B", "delivered", 1, 4},
			{"B", "hard", 1, 5},
			{"B", "sent", 1, 6},
			{"C", "soft", 1, 7},
			{"A", "soft", 1, 99}, // duplicate
			{"C", "delivered", 1, 40},
		}
		for _, s := range steps {
			outs = append(outs, recv(t, tr, "m", s.rcpt, s.kind, s.attempt, s.ts))
		}
		expired, err := tr.Tick(50)
		if err != nil {
			t.Fatal(err)
		}
		expired2, err := tr.Tick(60)
		if err != nil || len(expired2) != 0 {
			t.Fatalf("second tick = %v, %v", expired2, err)
		}
		st := status(t, tr, "m")
		return outs, st, expired
	}

	outs1, st1, exp1 := play()
	outs2, st2, exp2 := play()
	if fmt.Sprint(outs1) != fmt.Sprint(outs2) {
		t.Fatalf("outcomes differ: %v vs %v", outs1, outs2)
	}
	if fmt.Sprint(st1) != fmt.Sprint(st2) {
		t.Fatalf("status differs: %+v vs %+v", st1, st2)
	}
	if fmt.Sprint(exp1) != fmt.Sprint(exp2) {
		t.Fatalf("expiry list differs: %v vs %v", exp1, exp2)
	}
	t.Logf("replay outcomes=%v expired=%v summary=%s", outs1, exp1, st1.Summary)
}
