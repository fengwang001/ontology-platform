package api_test

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"ontology/api"
)

func rng(a, b int64) []int64 {
	s := make([]int64, b-a+1)
	for i := range s {
		s[i] = a + int64(i)
	}
	return s
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func TestEightStepGolden(t *testing.T) {
	p, _ := api.New(2, 5, 10, 100)
	type row struct {
		enq, out []int64
		adv      int64
		em       bool
		n, tk, q int64
	}
	rows := []row{
		{enq: rng(1, 15), n: 0, tk: 10, q: 15},
		{em: true, n: 0, tk: 0, q: 5, out: rng(1, 10)},
		{adv: 1, n: 1, tk: 5, q: 5},
		{em: true, n: 1, tk: 0, q: 0, out: rng(11, 15)},
		{adv: 2, n: 2, tk: 2, q: 0},
		{em: true, n: 2, tk: 2, q: 0, out: []int64{}},
		{enq: []int64{16, 17, 18}, n: 2, tk: 2, q: 3},
		{em: true, n: 2, tk: 0, q: 1, out: []int64{16, 17}},
	}
	for i, s := range rows {
		var got []int64
		if s.enq != nil {
			must(t, p.Enqueue(s.enq...))
		} else if s.adv != 0 {
			must(t, p.Advance(s.adv))
		} else if s.em {
			got = p.Emit()
		}
		if p.Now() != s.n || p.Tokens() != s.tk || int64(p.Pending()) != s.q || !slices.Equal(got, s.out) {
			t.Fatalf("step %d: (%d,%d,%d) %v want (%d,%d,%d) %v", i+1, p.Now(), p.Tokens(), p.Pending(), got, s.n, s.tk, s.q, s.out)
		}
	}
}
func TestCatchUpRate(t *testing.T) {
	for _, c := range []struct {
		to, want int64
		back     bool
	}{{1, 5, true}, {3, 10, true}, {4, 6, false}, {9, 10, false}} {
		p, _ := api.New(2, 5, 10, 100)
		must(t, p.Enqueue(rng(1, 15)...))
		p.Emit()
		if !c.back {
			must(t, p.Advance(1))
			p.Emit()
		}
		if e := p.Advance(c.to); e != nil || p.Tokens() != c.want {
			t.Fatalf("%+v: tok=%d err=%v", c, p.Tokens(), e)
		}
	}
}
func TestConservation(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 100; iter++ {
		rate := int64(1 + r.Intn(4))
		p, _ := api.New(rate, rate+int64(r.Intn(6)), int64(1+r.Intn(10)), 30)
		var in, out, next, now int64
		for n := 0; n < 200; n++ {
			switch r.Intn(3) {
			case 0:
				k := int64(1 + r.Intn(4))
				if p.Enqueue(rng(next, next+k-1)...) == nil {
					in, next = in+k, next+k
				}
			case 1:
				now += int64(r.Intn(3))
				_ = p.Advance(now)
			case 2:
				out += int64(len(p.Emit()))
			}
			if in != out+int64(p.Pending()) {
				t.Fatalf("iter %d step %d: %d != %d+%d", iter, n, in, out, p.Pending())
			}
		}
	}
}
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	for _, b := range [][4]int64{{0, 5, 10, 4}, {6, 5, 10, 4}, {2, 5, 0, 4}, {2, 5, 10, 0}, {-1, 5, 10, 4}} {
		if _, e := api.New(b[0], b[1], b[2], int(b[3])); !errors.Is(e, api.ErrInvalidParam) {
			t.Fatalf("%v: want ErrInvalidParam, got %v", b, e)
		}
	}
	p, _ := api.New(2, 5, 10, 3)
	must(t, p.Enqueue(1, 2, 3))
	n0, t0, p0 := p.Now(), p.Tokens(), p.Pending()
	if e := p.Advance(-1); !errors.Is(e, api.ErrClockRollback) {
		t.Fatalf("want ErrClockRollback, got %v", e)
	}
	if e := p.Enqueue(4); !errors.Is(e, api.ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", e)
	}
	if p.Now() != n0 || p.Tokens() != t0 || p.Pending() != p0 || len(p.Emit()) != 3 {
		t.Fatal("rejected op mutated state or pacer unusable")
	}
	if api.ErrClockRollback == api.ErrQueueFull || api.ErrClockRollback == api.ErrInvalidParam ||
		api.ErrQueueFull == api.ErrInvalidParam {
		t.Fatal("sentinel errors are not distinct")
	}
}
func TestConcurrentReaders(t *testing.T) {
	p, _ := api.New(3, 7, 9, 100)
	must(t, p.Enqueue(rng(1, 50)...))
	must(t, p.Advance(4))
	p.Emit()
	want := [3]int64{int64(p.Pending()), p.Tokens(), p.Now()}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got := [3]int64{int64(p.Pending()), p.Tokens(), p.Now()}
				if got != want {
					t.Errorf("reader saw %v, want %v", got, want)
				}
			}
		}()
	}
	wg.Wait()
}
func TestSelfCheck(t *testing.T) {
	p, _ := api.New(2, 5, 10, 100)
	must(t, p.SelfCheck())
}
