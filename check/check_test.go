package check_test

import (
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/check"
	"ontology/queue"
)

func TestFIFO(t *testing.T) { // 语义 1/2/3/5：保序、空队、惰性转移、边界
	cases := []struct {
		name string
		ops  []int
	}{
		{"1..5 then drain", []int{1, 2, 3, 4, 5, -1, -1, -1, -1, -1}},
		{"empty/single dequeue/peek", []int{-1, -2, 7, -2, -1, -2}},
		{"alternating 10000", slices.Repeat([]int{7, -1}, 5000)},
	}
	for _, c := range cases {
		if msg, ok := check.Verify(c.ops); !ok {
			t.Fatalf("%s: %s", c.name, msg)
		}
	}
}

func TestMoves(t *testing.T) { // 复杂度：摊还 <=2；错误实现单次操作与 n 成正比
	var q queue.Queue[int]
	for i := 0; i < 10000; i++ {
		if rand.Intn(2) == 0 {
			q.Enqueue(i)
		} else {
			q.Dequeue()
		}
	}
	if q.Moves() > 20000 {
		t.Fatalf("amortized moves=%d > 2*10000", q.Moves())
	}
	for _, n := range []int{100, 1000} {
		var b badQueue
		for i := 0; i < n; i++ {
			b.Enqueue(i)
		}
		for i := 0; i < n; i++ {
			b.Dequeue()
			b.Enqueue(i)
		}
		if per := b.moves / (3 * n); per < n/2 {
			t.Fatalf("n=%d: per-op moves=%d, want O(n)", n, per)
		}
	}
}

// badQueue 内联错误实现：每次 Dequeue 都倒、Enqueue 又倒回。
type badQueue struct {
	in, out []int
	moves   int
}

func (b *badQueue) Enqueue(v int) { // 错误：入队前把 out 倒回 in
	b.moves += len(b.out)
	for i := len(b.out) - 1; i >= 0; i-- {
		b.in = append(b.in, b.out[i])
	}
	b.out = b.out[:0]
	b.in = append(b.in, v)
}
func (b *badQueue) Dequeue() { // 错误：出队前把 in 全部倒入 out
	b.moves += len(b.in)
	for i := len(b.in) - 1; i >= 0; i-- {
		b.out = append(b.out, b.in[i])
	}
	b.in = b.in[:0]
	b.out = b.out[:len(b.out)-1] // 测试负载保证非空
}
func TestConcurrentReads(t *testing.T) { // 并发：16 读者只读 Peek/Len，写串行保护
	cases := []struct{ readers, writes int }{{16, 1000}}
	for _, c := range cases {
		var q queue.Queue[int]
		var wg sync.WaitGroup
		for g := 0; g < c.readers; g++ {
			wg.Go(func() {
				for i := 0; i < 2000; i++ {
					q.Peek()
					q.Len()
				}
			})
		}
		for i := 0; i < c.writes; i++ {
			q.Enqueue(i)
			q.Dequeue()
		}
		wg.Wait()
		if _, ok := q.Peek(); ok || q.Len() != 0 {
			t.Fatal("final state not empty")
		}
	}
}
