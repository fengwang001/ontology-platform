package median

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentQueriesAndCommits(t *testing.T) {
	tr := NewTracker(0)
	for _, v := range []int{4, 1, 7, 3, 6} {
		if err := tr.Add(v); err != nil {
			t.Fatal(err)
		}
	}

	var readers sync.WaitGroup
	var writers sync.WaitGroup
	stop := make(chan struct{})

	// 查询与自检执行体：多个 goroutine 并发只读，可与提交并发。
	for r := 0; r < 6; r++ {
		readers.Add(1)
		go func(id int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := tr.Median(); err != nil && !errors.Is(err, ErrEmpty) {
					t.Errorf("reader %d median: %v", id, err)
					return
				}
				_ = tr.Len()
				if msg, err := tr.Check(); err != nil || msg != "" {
					t.Errorf("reader %d check: %q err=%v", id, msg, err)
					return
				}
			}
		}(r)
	}

	// 提交执行体：整批混合加入/撤回，提交始终保持非空。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 3000; i++ {
			v := (i % 5) + 1
			err := tr.Commit([]Op{
				{Kind: OpAdd, Value: v},
				{Kind: OpAdd, Value: v + 10},
				{Kind: OpWithdraw, Value: v},
			})
			if err != nil {
				t.Errorf("writer commit %d: %v", i, err)
				return
			}
		}
	}()

	// 提交执行体：只加入，制造额外竞争。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 1000; i++ {
			if err := tr.Add(100 + i%7); err != nil {
				t.Errorf("writer add %d: %v", i, err)
				return
			}
		}
	}()

	writers.Wait()
	close(stop)
	readers.Wait()

	if msg, err := tr.Check(); err != nil || msg != "" {
		t.Fatalf("post-concurrency check: %q %v", msg, err)
	}
	t.Logf("并发结束: len=%d，Median/Len/Check 与 Commit 并发无竞态且自检通过；依据=RWMutex 下读者并发、提交独占并始终维护不变量", tr.Len())
}
