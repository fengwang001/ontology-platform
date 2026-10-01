package ontology

import (
	"sync"
	"testing"
)

// 两个并发 Begin 恰有一个成功；并发读等价某串行顺序。
func TestConcurrentBegin(t *testing.T) {
	tbl, err := NewTable(false, false)
	if err != nil {
		t.Fatal(err)
	}

	const n = 32
	var wg sync.WaitGroup
	results := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			results <- tbl.Begin()
		}()
	}

	// 并发只读访问，始终应安全且视图合法
	stop := make(chan struct{})
	var readerWg sync.WaitGroup
	readerWg.Add(2)
	for r := 0; r < 2; r++ {
		go func() {
			defer readerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = tbl.Keys()
					_, _ = tbl.Get("a")
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readerWg.Wait()
	close(results)

	success, active := 0, 0
	for e := range results {
		if e == nil {
			success++
		} else if e.(*Error).Reason == ReasonTransactionActive {
			active++
		} else {
			t.Fatalf("unexpected Begin error: %v", e)
		}
	}
	if success != 1 || active != n-1 {
		t.Fatalf("expected exactly 1 successful Begin, got success=%d active=%d", success, active)
	}

	if err := tbl.Rollback(); err != nil {
		t.Fatal(err)
	}
}

// 并发串行竞争同一事务生命周期：结果等价某个串行顺序，
// 最终已提交状态绝无重复非空键。
func TestConcurrentOpsSerialEquivalence(t *testing.T) {
	tbl, err := NewTable(false, false)
	if err != nil {
		t.Fatal(err)
	}

	const writers = 16
	var wg sync.WaitGroup

	// 每个 worker 反复尝试 Begin，抢到事务后尝试插入同一个键，
	// 无论成功失败都结束事务（Commit 或 Rollback）。
	wg.Add(writers)
	for i := 0; i < writers; i++ {
		go func(id int) {
			defer wg.Done()
			if tbl.Begin() != nil {
				return
			}
			row := "row" + string(rune('0'+id))
			err := tbl.Apply([]Op{Insert(row, StringKey("shared"))})
			if err == nil {
				err = tbl.Commit()
			}
			if err != nil {
				_ = tbl.Rollback()
			}
		}(i)
	}
	wg.Wait()

	keys := tbl.Keys()
	seen := map[string]int{}
	for _, rk := range keys {
		if !rk.Key.IsNull {
			seen[rk.Key.Value]++
		}
	}
	for k, c := range seen {
		if c > 1 {
			t.Fatalf("committed state has duplicated key %q: %v", k, keys)
		}
	}
	if seen["shared"] != 1 {
		t.Fatalf("exactly one writer should commit key shared, got %d", seen["shared"])
	}
}
