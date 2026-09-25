package view

import (
	"fmt"
	"sync"
	"testing"
)

// 复杂度：Commit 只换指针，复制 cell 数恒为 0，不随 m 增长。
func TestCommitCopiesZero(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		v := New()
		must(t, v.StartRefresh())
		for i := 0; i < m; i++ {
			must(t, v.Stage(fmt.Sprintf("c%d", i), "v"))
		}
		must(t, v.Commit())
		must(t, v.StartRefresh())
		must(t, v.Commit()) // 不 Stage 任何写
		if v.lastCommitCells != 0 {
			t.Fatalf("m=%d: commit copied %d cells, want 0", m, v.lastCommitCells)
		}
	}
}

// 并发：N 个读者 + 1 个写者 M 轮提交；每个 (seq,cells) 自洽，最终 Seq==M。
func TestConcurrentReadCommit(t *testing.T) {
	const N, M = 8, 200
	v := New()
	var wg sync.WaitGroup
	start := make(chan struct{})
	bad := make(chan string, N)
	for r := 0; r < N; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < M; i++ {
				seq, cells := v.Read()
				_, hasCur := cells[fmt.Sprintf("k%d", seq)]
				_, hasNext := cells[fmt.Sprintf("k%d", seq+1)]
				if int64(len(cells)) != seq || (seq > 0 && !hasCur) || hasNext {
					bad <- fmt.Sprintf("mixed pair: seq=%d len=%d", seq, len(cells))
					return
				}
			}
		}()
	}
	close(start)
	for i := 1; i <= M; i++ {
		must(t, v.StartRefresh())
		must(t, v.Stage(fmt.Sprintf("k%d", i), "v"))
		must(t, v.Commit())
	}
	wg.Wait()
	select {
	case msg := <-bad:
		t.Fatal(msg)
	default:
	}
	if seq, _ := v.Read(); seq != M {
		t.Fatalf("final seq=%d, want %d", seq, M)
	}
}
