package rank

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/iter"
	"ontology/key"
	"ontology/list"
)

func build(t *testing.T, keys ...key.Key) *list.List {
	l := list.New(32, 1<<20)
	for _, k := range keys {
		if err := l.Insert(k); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestAtRankOfRange(t *testing.T) {
	r := New(build(t, "b", "a", "c", "b", "d", "b")) // sorted: a b b b c d
	atCases := []struct {
		k    int
		want key.Key
		err  error
	}{
		{0, "a", nil}, {1, "b", nil}, {3, "b", nil}, {5, "d", nil},
		{-1, "", ErrOutOfRange}, {6, "", ErrOutOfRange}, {99, "", ErrOutOfRange},
	}
	for _, c := range atCases {
		if got, err := r.At(c.k); got != c.want || err != c.err {
			t.Errorf("At(%d)=%v,%v want %v,%v", c.k, got, err, c.want, c.err)
		}
	}
	rankCases := []struct {
		k    key.Key
		want int
	}{
		{"a", 0}, {"b", 1}, {"c", 4}, {"d", 5},
		{"", 0}, {"aa", 1}, {"bb", 4}, {"zz", 6},
	}
	for _, c := range rankCases {
		if got := r.RankOf(c.k); got != c.want {
			t.Errorf("RankOf(%q)=%d want %d", c.k, got, c.want)
		}
	}
	rangeCases := []struct {
		lo, hi key.Key
		want   int
		err    error
	}{
		{"a", "d", 5, nil}, {"b", "b", 0, nil},
		{"aa", "cz", 4, nil}, {"d", "a", 0, ErrBadRange},
	}
	for _, c := range rangeCases {
		if got, err := r.Range(c.lo, c.hi); got != c.want || err != c.err {
			t.Errorf("Range(%q,%q)=%d,%v want %d,%v", c.lo, c.hi, got, err, c.want, c.err)
		}
	}
}

func TestVisitedSublinear(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		l := list.New(32, 1<<20)
		for i := 0; i < n; i++ {
			if err := l.Insert(key.Key(fmt.Sprintf("k%06d", i))); err != nil {
				t.Fatal(err)
			}
		}
		r := New(l)
		_, _ = r.At(n / 2)
		atV := r.visited.Load()
		_ = r.RankOf(key.Key(fmt.Sprintf("k%06d", n/2)))
		rankV := r.visited.Load()
		if atV > 80 || rankV > 80 {
			t.Errorf("n=%d: At visited %d, RankOf visited %d; want <= 80", n, atV, rankV)
		}
		t.Logf("n=%d: At visited %d nodes, RankOf visited %d nodes", n, atV, rankV)
	}
}

func TestIterator(t *testing.T) {
	var got []key.Key
	for it := iter.New(build(t, "c", "a", "b", "a")); ; {
		k, ok, err := it.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		got = append(got, k)
	}
	if want := []key.Key{"a", "a", "b", "c"}; !slices.Equal(got, want) {
		t.Fatalf("iteration=%v want %v", got, want)
	}
	writes := map[string]func(*list.List, key.Key){
		"insert":         func(l *list.List, k key.Key) { _ = l.Insert("zz") },
		"delete-current": func(l *list.List, k key.Key) { _ = l.Delete(k) },
	}
	for name, write := range writes {
		l := build(t, "a", "b", "c")
		it := iter.New(l)
		cur, _, _ := it.Next()
		write(l, cur)
		if _, _, err := it.Next(); !errors.Is(err, iter.ErrInvalidated) {
			t.Errorf("%s: got %v want ErrInvalidated", name, err)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	l := list.New(32, 1<<20)
	for i := 0; i < 2000; i++ {
		if err := l.Insert(key.Key(fmt.Sprintf("k%06d", (i*7919)%2000))); err != nil {
			t.Fatal(err)
		}
	}
	r := New(l)
	const g = 8
	got := make([][]key.Key, g)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for w := range g {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := 0; j < 100; j++ {
				k, _ := r.At((j * 7) % l.Len())
				got[w] = append(got[w], k)
				_ = r.RankOf(k)
				_, _ = r.Range("k000100", "k000200")
				_ = l.Count(k)
			}
		}()
	}
	close(start)
	wg.Wait()
	for w := 1; w < g; w++ {
		if !slices.Equal(got[0], got[w]) {
			t.Fatalf("goroutine %d results differ", w)
		}
	}
}
