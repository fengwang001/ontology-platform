package check

import (
	"errors"
	"sync"
	"testing"

	"ontology/queue"
	"ontology/stack"
)

func TestFIFO(t *testing.T) {
	q := queue.New[int]()
	for _, v := range [5]int{1, 2, 3, 4, 5} {
		_ = q.Enqueue(v)
	}
	for _, w := range [5]int{1, 2, 3, 4, 5} {
		if g, ok := q.Dequeue(); !ok || g != w {
			t.Fatalf("%d,%v want %d", g, ok, w)
		}
	}
}

func TestReference(t *testing.T) {
	for _, c := range [][2]int{{1, 1}, {5000, 2}, {10000, 3}, {10000, 4}} {
		q := queue.New[int]()
		ok, n := Match(q, c[0], int64(c[1]))
		if !ok || n != c[0] || q.Len() != 0 {
			t.Fatalf("c=%v ok=%v n=%d", c, ok, n)
		}
	}
}

func TestEmpty(t *testing.T) {
	q := queue.New[int]()
	if v, ok := q.Dequeue(); ok || v != 0 {
		t.Fatalf("Dequeue=(%v,%v)", v, ok)
	}
	if v, ok := q.Peek(); ok || v != 0 {
		t.Fatalf("Peek=(%v,%v)", v, ok)
	}
	_ = q.Enqueue(7)
	if v, ok := q.Dequeue(); !ok || v != 7 {
		t.Fatalf("Dequeue=(%v,%v) want 7", v, ok)
	}
}

func TestAmortized(t *testing.T) {
	const n = 10000
	q := queue.New[int]()
	q.ResetMoves()
	if ok, _ := Match(q, n, 99); !ok {
		t.Fatal("diverged")
	}
	if m := q.Moves(); m > 2*n {
		t.Fatalf("moves=%d > %d", m, 2*n)
	}
}

// moveAll 是事故版“每次都全量倒栈”：n 元素单次转移 n 次（O(n) 退化）。
func moveAll[T any](s, d *stack.Stack[T]) (m int) {
	for s.Len() > 0 {
		v, _ := s.Pop()
		_ = d.Push(v)
		m++
	}
	return
}

func TestBadDegenerate(t *testing.T) {
	prev := 0
	for _, n := range []int{50, 100, 200} {
		in, _ := stack.New[int]()
		out, _ := stack.New[int]()
		for j := 0; j < n; j++ {
			_ = in.Push(j)
		}
		m := moveAll(in, out)
		if _, e := out.Pop(); e != nil || m != n || m <= prev {
			t.Fatalf("n=%d m=%d prev=%d e=%v", n, m, prev, e)
		}
		prev = m
	}
}

func TestSentinels(t *testing.T) {
	empty, _ := stack.New[int]()
	var z *stack.Stack[int]
	_, e1 := empty.Pop()
	_, e2 := z.Pop()
	_, e3 := stack.New[int](-1)
	for _, x := range [][2]error{{e1, stack.ErrEmpty}, {e2, stack.ErrNilReceiver}, {e3, stack.ErrBadCapacity}} {
		if !errors.Is(x[0], x[1]) {
			t.Fatalf("%v != %v", x[0], x[1])
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	q := queue.New[int]()
	for i := 0; i < 1000; i++ {
		_ = q.Enqueue(i)
	}
	h, _ := q.Peek()
	l := q.Len()
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				v, ok := q.Peek()
				if !ok || v != h || q.Len() != l {
					t.Errorf("bad read %d %v", v, ok)
					return
				}
			}
		}()
	}
	wg.Wait()
}
