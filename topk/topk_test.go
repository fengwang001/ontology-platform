package topk

import (
	"reflect"
	"sort"
	"strconv"
	"sync"
	"testing"

	"ontology/wtop"
)

func ev(k string, s int64) wtop.Change { return wtop.Change{Key: k, Score: s} }

// naive is an independent brute-force reference written in the test.
func naive(w *wtop.Window, k int) []Entry {
	var r []Entry
	w.Range(func(key string, sum int64) bool { r = append(r, Entry{Key: key, Sum: sum}); return true })
	sort.Slice(r, func(i, j int) bool {
		return r[i].Sum > r[j].Sum || (r[i].Sum == r[j].Sum && r[i].Key < r[j].Key)
	})
	return r[:min(len(r), k)]
}

func TestWindowSums(t *testing.T) {
	cases := []struct {
		name       string
		n          int
		evs        []wtop.Change
		key        string
		wantSum    int64
		wantExists bool
		wantLen    int
	}{
		{"replace same key", 1, []wtop.Change{ev("a", 1), ev("a", 2)}, "a", 2, true, 1},
		{"evicted oldest", 2, []wtop.Change{ev("a", 3), ev("b", -1), ev("a", 4)}, "a", 4, true, 2},
		{"zero sum still exists", 2, []wtop.Change{ev("z", 5), ev("z", -5)}, "z", 0, true, 2},
		{"key fully leaves", 2, []wtop.Change{ev("a", 9), ev("b", 1), ev("c", 2)}, "a", 0, false, 2},
		{"negative only", 3, []wtop.Change{ev("q", -7), ev("q", -2)}, "q", -9, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := wtop.New(c.n)
			for _, e := range c.evs {
				w.Add(e)
			}
			sum, ok := w.Sum(c.key)
			if sum != c.wantSum || ok != c.wantExists || w.Len() != c.wantLen {
				t.Fatalf("got sum=%d exists=%v len=%d, want %d %v %d", sum, ok, w.Len(), c.wantSum, c.wantExists, c.wantLen)
			}
		})
	}
}

func TestNaiveReference(t *testing.T) {
	cases := []struct {
		name string
		n, k int
		num  int
	}{
		{"small negatives", 4, 2, 30},
		{"ties dense", 5, 3, 40},
		{"wide keys", 50, 10, 300},
		{"k equals n", 6, 6, 50},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := New(c.k, c.n)
			w := m.win
			state := int64(3)
			for i := 0; i < c.num; i++ {
				state = (state*1103515245 + 12345) & 0x7fffffff
				key := "k" + strconv.Itoa(int(state%int64(c.n*2)))
				m.Apply(ev(key, int64(state%21)-10))
				if got := m.TopK(); !reflect.DeepEqual(got, naive(w, c.k)) {
					t.Fatalf("step %d: got %v want %v", i, got, naive(w, c.k))
				}
			}
		})
	}
}

func TestWithdrawFill(t *testing.T) {
	seq := []wtop.Change{ev("a", 6), ev("a", 4), ev("b", 9), ev("c", 8), ev("d", 7), ev("e", 1), ev("f", 8)}
	checks := map[int][]Entry{
		5: {{Key: "a", Sum: 10}, {Key: "b", Sum: 9}},
		6: {{Key: "b", Sum: 9}, {Key: "c", Sum: 8}}, // a withdraws, c fills
		7: {{Key: "b", Sum: 9}, {Key: "c", Sum: 8}}, // c/f tie at 8: lexicographic c
	}
	m := New(2, 5)
	for i, e := range seq {
		m.Apply(e)
		if want, ok := checks[i+1]; ok && !reflect.DeepEqual(m.TopK(), want) {
			t.Fatalf("step %d: got %v want %v", i+1, m.TopK(), want)
		}
	}
}

func TestPopBound(t *testing.T) {
	for _, n := range []int{100, 1000, 10000} {
		m := New(10, n)
		for i := 0; i < n; i++ {
			m.Apply(ev("key"+strconv.Itoa(i), int64(i*7+1)))
		}
		m.TopK()
		if got := m.pops.Load(); got > int64(10+4) {
			t.Fatalf("m=%d pops=%d, want <= K+4", n, got)
		}
	}
}

func TestConcurrent(t *testing.T) {
	m := New(10, 1000)
	for i := 0; i < 1000; i++ {
		m.Apply(ev("key"+strconv.Itoa(i%77), int64(i%13)-6))
	}
	want := m.TopK()
	const readers = 16
	var wg sync.WaitGroup
	results := make([][]Entry, readers)
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for r := 0; r < 200; r++ {
				results[idx] = m.TopK()
			}
		}(g)
	}
	wg.Wait()
	for g, got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("reader %d: got %v want %v", g, got, want)
		}
	}
}
