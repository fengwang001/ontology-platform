package tob

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/seq"
)

func TestSeqCounter(t *testing.T) {
	c := seq.NewCounter()
	for w := 1; w <= 5; w++ {
		if g := c.Allocate(); g != w || c.Next() != w+1 {
			t.Fatalf("alloc=%d next=%d want %d/%d", g, c.Next(), w, w+1)
		}
	} // c.Next()==6；三元组 {seq, deliveredUpTo, 在空洞内?}，验开区间 (d,n)
	for _, x := range [][3]int{{3, 2, 1}, {2, 2, 0}, {6, 2, 0}, {1, 2, 0}, {7, 2, 0}} {
		if got := c.InGap(x[0], x[1]); got != (x[2] == 1) {
			t.Errorf("InGap(%d,%d)=%v want %d", x[0], x[1], got, x[2])
		}
	}
}
func TestSequence(t *testing.T) {
	l := New()
	type step struct {
		op   byte
		pay  string
		d, n int
	}
	steps := []step{{'P', "A", 0, 2}, {'P', "B", 0, 3}, {'D', "A", 1, 3}, {'P', "C", 1, 4}, {'D', "B", 2, 4}, {'P', "D", 2, 5}, {'P', "E", 2, 6}}
	var got []string
	for i, st := range steps {
		if st.op == 'P' {
			if _, err := l.Propose(st.pay); err != nil {
				t.Fatal(err)
			}
		} else if s, p, ok := l.Deliver(); !ok || p != st.pay {
			t.Fatalf("step %d (%d,%q,%v) want %q", i+1, s, p, ok, st.pay)
		} else {
			got = append(got, p)
		}
		if l.Delivered() != st.d || l.nextSeq() != st.n {
			t.Fatalf("step %d d/n=%d/%d want %d/%d", i+1, l.Delivered(), l.nextSeq(), st.d, st.n)
		}
	}
	l.Crash()
	if l.Delivered() != 2 || l.nextSeq() != 6 {
		t.Fatalf("crash d/n=%d/%d want 2/6", l.Delivered(), l.nextSeq())
	}
	if _, _, ok := l.Deliver(); ok {
		t.Fatal("deliver into unfilled gap must be empty")
	}
	type rp struct {
		s   int
		pay string
	}
	for _, r := range []rp{{4, "D"}, {3, "C"}, {5, "E"}} { // 乱序补发
		if err := l.RePropose(r.s, r.pay); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []string{"C", "D", "E"} {
		if _, p, ok := l.Deliver(); !ok || p != want {
			t.Fatalf("replay got %q/%v want %q", p, ok, want)
		} else {
			got = append(got, p)
		}
	}
	if fmt.Sprint(got) != "[A B C D E]" || l.Delivered() != 5 {
		t.Fatalf("delivery=%v d=%d", got, l.Delivered())
	}
}
func TestSelfCheck(t *testing.T) {
	l := New()
	for range 3 {
		if err := l.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck: %v", err)
		}
		if l.Delivered() != 0 || l.nextSeq() != 1 {
			t.Fatal("SelfCheck must not mutate receiver")
		}
	}
}
func TestFaultInjection(t *testing.T) {
	l := New()
	l.Propose("m")
	l.Deliver()
	l.Propose("z")
	l.Crash() // d=1 n=3 槽位 2 空
	if err := l.RePropose(2, "z2"); err != nil {
		t.Fatal(err)
	}
	type tc struct {
		name string
		call func() error
		want error
	}
	for _, c := range []tc{{"empty", func() error { _, e := l.Propose(""); return e }, ErrEmptyPayload}, {"delivered", func() error { return l.RePropose(1, "x") }, ErrAlreadyDelivered}, {"range", func() error { return l.RePropose(3, "x") }, ErrSeqOutOfRange}, {"filled", func() error { return l.RePropose(2, "x") }, ErrSlotFilled}} {
		d, n, ln := l.Delivered(), l.nextSeq(), len(l.entries)
		if err := c.call(); !errors.Is(err, c.want) {
			t.Errorf("%s: %v want %v", c.name, err, c.want)
		}
		if l.Delivered() != d || l.nextSeq() != n || len(l.entries) != ln {
			t.Errorf("%s mutated state", c.name)
		}
	}
	if s, p, ok := l.Deliver(); !ok || s != 2 || p != "z2" {
		t.Fatalf("after rejects deliver=(%d,%q,%v)", s, p, ok)
	}
}
func TestDeliverO1Scan(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		l := New()
		for range m {
			l.Propose("x")
		}
		if s, _, ok := l.Deliver(); !ok || s != 1 || l.scanCount() != 1 {
			t.Fatalf("m=%d scan=%d want 1", m, l.scanCount())
		}
	}
	l := New()
	if _, _, ok := l.Deliver(); ok || l.scanCount() != 0 {
		t.Fatal("empty deliver must scan 0")
	}
}
func TestConcurrentPropose(t *testing.T) {
	for _, N := range []int{50, 200, 500} {
		l, seqs, wg := New(), make([]int, N), sync.WaitGroup{}
		for i := range N {
			wg.Add(1)
			go func(i int) { defer wg.Done(); s, _ := l.Propose(fmt.Sprintf("p%d", i)); seqs[i] = s }(i)
		}
		wg.Wait()
		seen := make(map[int]bool, N)
		for _, s := range seqs {
			if seen[s] {
				t.Fatalf("N=%d duplicate %d", N, s)
			}
			seen[s] = true
		}
		for s := 1; s <= N; s++ {
			d, _, ok := l.Deliver()
			if !seen[s] || !ok || d != s {
				t.Fatalf("N=%d slot %d: seen=%v deliver=%d/%v", N, s, seen[s], d, ok)
			}
		}
	}
}
