package deque

import (
	"testing"
)

func TestStealHalfRounding(t *testing.T) {
	cases := []struct{ n, want int }{
		{0, 0}, {1, 1}, {2, 1}, {3, 2}, {4, 2}, {7, 4}, {8, 4},
	}
	for _, c := range cases {
		d := New[int](100)
		for i := 0; i < c.n; i++ {
			d.Push(i)
		}
		got := d.StealHalf(nil)
		if len(got) != c.want {
			t.Fatalf("n=%d stolen=%d want %d", c.n, len(got), c.want)
		}
		for i, v := range got {
			if v != i { // 保持受害者队列里的先后顺序
				t.Fatalf("n=%d order broken at %d: %d", c.n, i, v)
			}
		}
	}
}

func TestOwnerLIFO(t *testing.T) {
	d := New[int](8)
	for i := 0; i < 5; i++ {
		d.Push(i)
	}
	for want := 4; want >= 0; want-- {
		v, ok := d.Pop()
		if !ok || v != want {
			t.Fatalf("Pop=%d,%v want %d", v, ok, want)
		}
	}
	if _, ok := d.Pop(); ok {
		t.Fatal("empty Pop must fail")
	}
	if err := d.Push(1); !err {
		t.Fatal("Push after drain must succeed")
	}
}

func TestCapacity(t *testing.T) {
	d := New[int](3)
	for i := 0; i < 3; i++ {
		if !d.Push(i) {
			t.Fatal("push within capacity failed")
		}
	}
	if d.Push(9) {
		t.Fatal("push over capacity must fail")
	}
}

func TestMoveCount(t *testing.T) {
	const ops = 100000
	d := New[int](256)
	for i := 0; i < ops; i++ {
		switch i % 4 {
		case 0:
			d.Push(i)
		case 1:
			d.Pop()
		case 2:
			d.StealHalf(nil)
		case 3:
			d.Push(i)
			d.Pop()
		}
	}
	if m := d.Moves(); m > 3*ops {
		t.Fatalf("moves=%d exceeds 3*ops=%d", m, 3*ops)
	}
}

func TestLastElementRace(t *testing.T) {
	const rounds = 10000
	for r := 0; r < rounds; r++ {
		d := New[int](4)
		d.Push(r)
		start := make(chan struct{})
		res := make(chan int, 2)
		go func() {
			<-start
			_, ok := d.Pop()
			if ok {
				res <- 1
			} else {
				res <- 0
			}
		}()
		go func() {
			<-start
			got := d.StealHalf(nil)
			res <- len(got)
		}()
		close(start)
		a, b := <-res, <-res
		if a+b != 1 {
			t.Fatalf("round %d: owner=%d stealer=%d sum=%d", r, a, b, a+b)
		}
	}
}
