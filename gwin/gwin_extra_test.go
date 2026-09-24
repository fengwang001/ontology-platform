package gwin

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 不变量 4：三类拒绝互不相同，任一条被拒则整批（含好元素）不留痕，之后仍可用。
func TestRejectedBatchAtomic(t *testing.T) {
	if errors.Is(ErrInvalidPeriod, ErrEmptyKey) || errors.Is(ErrInvalidPeriod, ErrSnapshotLimit) ||
		errors.Is(ErrEmptyKey, ErrSnapshotLimit) {
		t.Fatal("sentinel errors must be distinct")
	}
	if _, err := New(0, 10); err != ErrInvalidPeriod {
		t.Fatalf("period<=0: got %v", err)
	}
	q, _ := New(3, 100)
	_ = q.Feed([]Event{{Key: "k", Val: 1}, {Key: "k", Val: 2}}) // cnt=2，无快照
	s0, c0 := q.Totals("k")
	bad := [][]Event{
		{{Key: "z", Val: 1}, {Key: "", Val: 1}}, // 空 Key 在后
		{{Key: "", Val: 1}, {Key: "z", Val: 1}}, // 空 Key 在前
	}
	for i, b := range bad {
		if err := q.Feed(b); err != ErrEmptyKey {
			t.Fatalf("case %d: got %v", i, err)
		}
		if s, c := q.Totals("z"); s != 0 || c != 0 {
			t.Fatalf("case %d: rejected good element left trace (%d,%d)", i, s, c)
		}
		if s, c := q.Totals("k"); s != s0 || c != c0 || len(q.Snapshots("k")) != 0 {
			t.Fatalf("case %d: existing state changed", i)
		}
	}
	l, _ := New(1, 1) // 快照超限：maxSnap=1，第 2 次触发必须整体拒绝
	if err := l.Feed([]Event{{Key: "q", Val: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := l.Feed([]Event{{Key: "q", Val: 1}}); err != ErrSnapshotLimit {
		t.Fatalf("limit: got %v", err)
	}
	if s, c := l.Totals("q"); s != 1 || c != 1 || len(l.Snapshots("q")) != 1 {
		t.Fatalf("limit rejection changed state: (%d,%d) snaps=%d", s, c, len(l.Snapshots("q")))
	}
	if err := l.Feed([]Event{{Key: "other", Val: 5}}); err != nil { // 被拒后仍可正常使用
		t.Fatalf("reuse after reject: %v", err)
	}
}

// 复杂度：多档 m（period 不整除 m），再喂 1 条恰好触发；
// 为成像读取的已累积元素个数 readCnt 恒为 0，不随 m 线性增长。
func TestSnapshotReadCountConstant(t *testing.T) {
	for _, m := range []int64{100, 1000, 10000} {
		q, _ := New(m+1, 5) // m % (m+1) != 0，前 m 条不触发
		batch := make([]Event, m)
		for i := range batch {
			batch[i] = Event{Key: "k", Val: 1}
		}
		if err := q.Feed(batch); err != nil {
			t.Fatal(err)
		}
		if err := q.Feed([]Event{{Key: "k", Val: 1}}); err != nil {
			t.Fatal(err)
		}
		e := q.keys["k"]
		if len(e.snaps) != 1 || e.readCnt != 0 {
			t.Fatalf("m=%d: snaps=%d readCnt=%d, want 1 snapshot and O(1) read 0", m, len(e.snaps), e.readCnt)
		}
	}
}

// 并发：N 个 goroutine 各向不同 Key 喂满；并发读不得见 sum/cnt 回退；
// 结束后 Totals 与单线程逐字段相同。不使用 sleep。
func TestConcurrentDistinctKeys(t *testing.T) {
	const N, perG = 24, 200
	q, _ := New(7, 100000)
	var wsum int64
	for v := int64(1); v <= perG; v++ {
		wsum += v
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for g := 0; g < N; g++ { // 读者：忙等轮询，最新快照只能增长不能回退
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			key := fmt.Sprintf("g%d", g)
			var lastCnt, lastSum int64
			started := false
			for {
				if ss := q.Snapshots(key); len(ss) > 0 {
					s := ss[len(ss)-1]
					if started && (s.Cnt < lastCnt || s.Sum < lastSum) {
						t.Errorf("regression on %s: (%d,%d) after (%d,%d)", key, s.Sum, s.Cnt, lastSum, lastCnt)
					}
					lastCnt, lastSum, started = s.Cnt, s.Sum, true
				}
				select {
				case <-stop:
					return
				default:
				}
			}
		}(g)
	}
	var fw sync.WaitGroup
	for g := 0; g < N; g++ { // 写者：每 Key 一个 goroutine
		fw.Add(1)
		go func(g int) {
			defer fw.Done()
			batch := make([]Event, perG)
			for v := range batch {
				batch[v] = Event{Key: fmt.Sprintf("g%d", g), Val: int64(v + 1)}
			}
			if err := q.Feed(batch); err != nil {
				t.Errorf("feed: %v", err)
			}
		}(g)
	}
	fw.Wait()
	close(stop)
	wg.Wait()
	for g := 0; g < N; g++ {
		s, c := q.Totals(fmt.Sprintf("g%d", g))
		if s != wsum || c != perG {
			t.Fatalf("g%d totals (%d,%d), want (%d,%d)", g, s, c, wsum, perG)
		}
	}
}
