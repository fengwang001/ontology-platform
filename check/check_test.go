package check

import (
	"slices"
	"sync"
	"testing"

	"ontology/skip"
)

func TestReproducible(t *testing.T) {
	cases := []struct {
		sa, sb   uint64
		wantSame bool
	}{{7, 7, true}, {7, 8, false}}
	keys := shuffled(1000, 42)
	for _, c := range cases {
		a, b := build(c.sa, keys), build(c.sb, keys)
		ra, _ := a.Range(0, 1000)
		rb, _ := b.Range(0, 1000)
		same := slices.Equal(a.Levels(), b.Levels()) && slices.Equal(ra, rb)
		if same != c.wantSame {
			t.Fatalf("seeds %d,%d: same=%v want %v", c.sa, c.sb, same, c.wantSame)
		}
	}
}

func TestRangeVsRef(t *testing.T) {
	cases := []struct{ lo, hi int }{{0, 500}, {10, 20}, {0, 1}, {499, 500}, {250, 250}, {600, 700}}
	keys := shuffled(500, 1)
	l, ref := build(3, keys), newRef(keys...)
	for _, c := range cases {
		got, err := l.Range(c.lo, c.hi)
		if c.lo >= c.hi {
			mustErr(t, err, skip.ErrBadRange)
			continue
		}
		if want := ref.Range(c.lo, c.hi); !slices.Equal(got, want) {
			t.Fatalf("Range(%d,%d) mismatch with ref", c.lo, c.hi)
		}
	}
}

func TestOpsAndErrors(t *testing.T) {
	keys := shuffled(200, 5)
	l := build(9, keys)
	mustErr(t, l.Insert(keys[0], 0), skip.ErrDuplicate)
	mustErr(t, l.Delete(999), skip.ErrNotFound)
	for _, k := range keys {
		if !findOK(l, k) {
			t.Fatalf("Find(%d) wrong", k)
		}
	}
	for _, k := range keys {
		_ = l.Delete(k)
	}
	_, ok := l.Find(keys[0])
	got, _ := l.Range(0, 200)
	if l.Len() != 0 || ok || len(got) != 0 {
		t.Fatal("after deleting all: Len/Find/Range must be empty")
	}
}

func TestComparesBound(t *testing.T) {
	cases := []struct{ n, finds, bound int }{{10000, 10000, 64}}
	for _, c := range cases {
		l := build(13, shuffled(c.n, 11))
		for i := 0; i < c.finds; i++ {
			_, _ = l.Find(i * 7 % c.n)
		}
		if avg := float64(l.Compares()) / float64(c.finds); avg > float64(c.bound) {
			t.Fatalf("n=%d avg compares %.1f > %d", c.n, avg, c.bound)
		}
	}
}

func TestConcurrentRead(t *testing.T) {
	l := build(17, shuffled(2000, 3))
	want, _ := l.Range(0, 2000)
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if k := (g*500 + i) % 2000; !findOK(l, k) {
					t.Errorf("Find(%d) mismatch", k)
				}
			}
			if got, _ := l.Range(0, 2000); !slices.Equal(got, want) {
				t.Error("Range mismatch")
			}
		}(g)
	}
	wg.Wait()
}
