package seqwin

import (
	"fmt"
	"sync"
	"testing"
)

// 语义 8：并发安全；每个序列号在所有 goroutine 中恰好 Fresh 一次。
func TestConcurrentExactlyOneFresh(t *testing.T) {
	const (
		goroutines = 16
		seqs       = 1000
	)
	// 窗口宽度等于批次大小，保证所有序列号始终在窗内，
	// 从而可以严格断言「每个序列号恰好一次 Fresh」。
	w := New(seqs)

	var wg sync.WaitGroup
	counts := make([]int32, seqs+1)
	start := make(chan struct{})
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for seq := 1; seq <= seqs; seq++ {
				if w.Accept(uint64(seq)) == Fresh {
					counts[seq]++
				}
			}
		}()
	}
	close(start)
	wg.Wait()

	for seq := 1; seq <= seqs; seq++ {
		if counts[seq] != 1 {
			t.Fatalf("seq %d judged Fresh %d times, want exactly 1", seq, counts[seq])
		}
	}
	if w.Highest() != seqs {
		t.Fatalf("Highest = %d, want %d", w.Highest(), seqs)
	}
}

// 并发混入大跳与旧序列号，配合 -race 验证内部状态无数据竞争。
func TestConcurrentMixed(t *testing.T) {
	w := New(8)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			<-start
			for i := 0; i < 500; i++ {
				seq := uint64((base*7+i*13)%5000 + 1)
				switch w.Accept(seq) {
				case Fresh, Duplicate, TooOld:
				default:
					t.Errorf("unexpected verdict for %d", seq)
				}
				_ = w.Highest()
				_ = w.Seen(seq)
			}
		}(g)
	}
	close(start)
	wg.Wait()
	if w.Highest() < 1 {
		t.Fatalf("Highest = %d, want >= 1", w.Highest())
	}
}

func TestVerdictString(t *testing.T) {
	cases := []struct {
		v    Verdict
		want string
	}{
		{Fresh, "Fresh"},
		{Duplicate, "Duplicate"},
		{TooOld, "TooOld"},
		{Invalid, "Invalid"},
	}
	for _, c := range cases {
		if got := fmt.Sprint(c.v); got != c.want {
			t.Fatalf("String() = %q, want %q", got, c.want)
		}
	}
}
