package engine

import (
	"fmt"
	"sync"
	"testing"
)

// replayed 恒等于 synced-committed，与已提交文档总数无关。
func TestReplayedIndependentOfCommittedCount(t *testing.T) {
	for _, committed := range []int{1000, 100000} {
		e := New(Async)
		for i := 0; i < committed; i++ {
			if _, err := e.Index(fmt.Sprintf("id-%06d", i), bb("v"), -1); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Flush(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 10; i++ {
			if _, err := e.Index(fmt.Sprintf("new-%d", i), bb("v"), -1); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Sync(); err != nil {
			t.Fatal(err)
		}
		if e.Uncommitted() != 10 {
			t.Fatalf("committed=%d 时未提交条数=%d, 期望 10", committed, e.Uncommitted())
		}
		if err := e.Crash(); err != nil {
			t.Fatal(err)
		}
		n, err := e.Recover()
		if err != nil {
			t.Fatal(err)
		}
		if n != 10 || e.replayed != int(e.Synced()-e.Committed()) {
			t.Fatalf("committed=%d: n=%d replayed=%d synced-committed=%d",
				committed, n, e.replayed, e.Synced()-e.Committed())
		}
	}
}

// 并发调用结果等价于某个串行顺序：序号 1..N 每个恰好一次，最终无丢失。
func TestConcurrentWriters(t *testing.T) {
	e := New(Request)
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	seqs := make([][]int64, writers)
	errs := make(chan error, writers*perWriter)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				seq, err := e.Index(fmt.Sprintf("w%d-%d", w, i), bb("x"), -1)
				if err != nil {
					errs <- err
					return
				}
				seqs[w] = append(seqs[w], seq)
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[int64]int{}
	total := 0
	for _, list := range seqs {
		for _, seq := range list {
			seen[seq]++
			total++
		}
	}
	if total != writers*perWriter || e.MaxSeq() != int64(total) {
		t.Fatalf("total=%d maxSeq=%d", total, e.MaxSeq())
	}
	for seq := int64(1); seq <= int64(total); seq++ {
		if seen[seq] != 1 {
			t.Fatalf("seq %d 出现 %d 次, 期望恰好一次", seq, seen[seq])
		}
	}
}
