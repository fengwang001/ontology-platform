package api

import (
	"errors"
	"math/rand"
	"slices"
	"sync"
	"testing"
)

func eqLog(got []any, want ...int64) bool {
	conv := make([]int64, len(got))
	for i, v := range got {
		conv[i] = v.(int64)
	}
	return slices.Equal(conv, want)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// The seven-step sequence from NOTES.md, checked step by step.
func TestSevenStep(t *testing.T) {
	seqs := []int64{0, 2, 1, 2, 3, 0, 4}
	logs := [][]int64{{0}, {0}, {0, 1, 2}, {0, 1, 2}, {0, 1, 2, 3}, {0, 1, 2, 3}, {0, 1, 2, 3, 4}}
	dups := []int{0, 0, 0, 1, 1, 2, 2} // step 3 cascades the buffered 2
	a := New(16)
	for i, s := range seqs {
		must(t, a.Deliver("S", s, s))
		if !eqLog(a.Delivered("S"), logs[i]...) || a.Dup("S") != dups[i] {
			t.Fatalf("step %d: log=%v dups=%d", i, a.Delivered("S"), a.Dup("S"))
		}
	}
}

func TestExactlyOnce(t *testing.T) {
	cases := [][]int64{{0, 2, 1, 2, 3, 0, 4}, {0, 0, 0, 1, 1, 2, 2}, {5, 5, 5, 0, 4, 4, 3, 2, 1}}
	for _, arr := range cases {
		a := New(16)
		for _, s := range arr {
			must(t, a.Deliver("S", s, s))
		}
		seen := map[int64]bool{}
		for _, v := range a.Delivered("S") {
			s := v.(int64)
			if seen[s] {
				t.Fatalf("arr=%v: seq %d delivered twice", arr, s)
			}
			seen[s] = true
		}
		if a.Dup("S") != len(arr)-len(seen) {
			t.Fatalf("arr=%v: dups=%d, want %d", arr, a.Dup("S"), len(arr)-len(seen))
		}
	}
}

// The delivered log is ascending and gap-free from 0.
func TestFIFOOrder(t *testing.T) {
	for _, n := range []int{1, 10, 100, 1000} {
		a := New(n)
		for _, p := range rand.New(rand.NewSource(int64(n))).Perm(n) {
			must(t, a.Deliver("S", int64(p), int64(p)))
		}
		for i, v := range a.Delivered("S") {
			if v.(int64) != int64(i) {
				t.Fatalf("n=%d: log[%d]=%v", n, i, v)
			}
		}
	}
}

// Random arrivals with duplicates match the naive reference.
func TestNaiveReference(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for _, n := range []int{5, 50, 500} {
		a := New(n)
		for _, sender := range []string{"x", "y", "z"} {
			var arr []int64
			for _, p := range rnd.Perm(n) { // all of 0..n-1, shuffled
				arr = append(arr, int64(p))
			}
			for i := 0; i < n; i++ { // plus random duplicates
				arr = append(arr, int64(rnd.Intn(n)))
			}
			rnd.Shuffle(len(arr), func(i, j int) { arr[i], arr[j] = arr[j], arr[i] })
			for _, s := range arr {
				must(t, a.Deliver(sender, s, s))
			}
			if !eqLog(a.Delivered(sender), naive(arr)...) {
				t.Fatalf("n=%d sender=%s: log != naive", n, sender)
			}
		}
	}
}

// Rejections are distinct, decidable, and change nothing.
func TestFailureNoTrace(t *testing.T) {
	a := New(1)
	must(t, a.Deliver("F", 0, int64(0))) // log [0]
	must(t, a.Deliver("F", 3, int64(3))) // buffer {3}, full
	senders := []string{"", "F", "F"}
	seqs := []int64{0, -1, 9}
	wants := []error{ErrEmptySender, ErrNegativeSeq, ErrBufferFull}
	for i := range wants {
		if err := a.Deliver(senders[i], seqs[i], nil); !errors.Is(err, wants[i]) {
			t.Fatalf("case %d: got %v, want %v", i, err, wants[i])
		}
	}
	if !eqLog(a.Delivered("F"), 0) || a.Dup("F") != 0 || a.Delivered("") != nil {
		t.Fatal("rejected delivery changed state")
	}
	for _, s := range []int64{1, 2, 4} { // still usable; buffered 3 cascades
		must(t, a.Deliver("F", s, s))
	}
	if !eqLog(a.Delivered("F"), 0, 1, 2, 3, 4) {
		t.Fatalf("log=%v", a.Delivered("F"))
	}
	must(t, a.SelfCheck())
}

func TestConcurrentDeliver(t *testing.T) {
	const n = 500
	a := New(n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, p := range rand.New(rand.NewSource(3)).Perm(n) {
		wg.Add(1)
		go func(s int64) {
			defer wg.Done()
			<-start
			if err := a.Deliver("S", s, s); err != nil {
				t.Error(err)
			}
		}(int64(p))
	}
	close(start)
	wg.Wait()
	for i, v := range a.Delivered("S") {
		if v.(int64) != int64(i) {
			t.Fatalf("log[%d]=%v", i, v)
		}
	}
	if a.Dup("S") != 0 {
		t.Fatalf("dups=%d, want 0", a.Dup("S"))
	}
}
