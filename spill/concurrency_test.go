package spill

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAppendCommitAndReaders：多个执行体并发执行 begin/append/commit，
// 同时有执行体并发调用下游日志、块查询、统计与自检，用 -race 验证并发安全。
func TestConcurrentAppendCommitAndReaders(t *testing.T) {
	const nTxn = 24
	const perTxn = 30
	m, err := NewManager(Config{MemRowLimit: 7, BlockLimit: 100000}, NewEventLog())
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	// 读者：在整个写入期间持续查询与自检。
	stop := make(chan struct{})
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = m.CommittedLog()
				_ = m.Blocks()
				_ = m.Stats()
				if err := m.CheckInvariants(); err != nil {
					t.Errorf("invariant violated: %v", err)
					return
				}
			}
		}()
	}

	// 写者：每个事务按序追加自己的唯一行并提交，校验输出行序。
	for id := uint64(1); id <= nTxn; id++ {
		wg.Add(1)
		go func(id uint64) {
			defer wg.Done()
			if err := m.Begin(id); err != nil {
				t.Errorf("begin %d: %v", id, err)
				return
			}
			want := make([]string, perTxn)
			for i := 0; i < perTxn; i++ {
				s := fmt.Sprintf("txn%d-row%d", id, i)
				want[i] = s
				if _, _, err := m.Append(id, []Row{{Data: s}}); err != nil {
					t.Errorf("append %d/%d: %v", id, i, err)
					return
				}
			}
			out, err := m.Commit(id)
			if err != nil {
				t.Errorf("commit %d: %v", id, err)
				return
			}
			if !equalStrings(dataOf(out), want) {
				t.Errorf("txn %d replay order mismatch: got %v want %v", id, dataOf(out), want)
			}
		}(id)
	}

	// 等待写者结束后再停读者。
	doneWriters := make(chan struct{})
	go func() {
		// WaitGroup 不可分组等待，这里另用计数：
		// 简化处理：轮询统计，直到所有事务结束。
		for {
			st := m.Stats()
			if st.OpenTxnCount == 0 && st.CommittedRows == nTxn*perTxn {
				close(doneWriters)
				return
			}
		}
	}()
	<-doneWriters
	close(stop)
	wg.Wait()

	if err := m.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
	log := m.CommittedLog()
	if len(log) != nTxn*perTxn {
		t.Fatalf("committed rows = %d, want %d", len(log), nTxn*perTxn)
	}
	// 每个事务的行在全局日志中必须保持内部顺序。
	seen := make(map[uint64]int)
	for _, r := range log {
		var id uint64
		var idx int
		if _, err := fmt.Sscanf(r.Data, "txn%d-row%d", &id, &idx); err != nil {
			t.Fatal(err)
		}
		if idx != seen[id] {
			t.Fatalf("txn %d rows out of order at global index: got %d want %d", id, idx, seen[id])
		}
		seen[id] = idx + 1
	}
	for id := uint64(1); id <= nTxn; id++ {
		if seen[id] != perTxn {
			t.Fatalf("txn %d committed %d rows, want %d", id, seen[id], perTxn)
		}
	}
}
