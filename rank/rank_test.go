package rank

import (
	"errors"
	"testing"

	"ontology/key"
	"ontology/list"
)

func buildList(t *testing.T, vals []int) *list.List {
	t.Helper()
	l := list.New(32, 1<<30)
	for _, v := range vals {
		if err := l.Insert(key.Key(v)); err != nil {
			t.Fatal(err)
		}
	}
	return l
}

func TestAtRankOfRange(t *testing.T) {
	l := buildList(t, []int{5, 1, 9, 1, 3, 7, 5}) // 排序后 1 1 3 5 5 7 9
	r := New(l)
	atCases := []struct {
		k    int
		want key.Key
	}{{0, 1}, {1, 1}, {2, 3}, {3, 5}, {4, 5}, {5, 7}, {6, 9}}
	for _, c := range atCases {
		got, err := r.At(c.k)
		if err != nil || got != c.want {
			t.Errorf("At(%d) = %v,%v 期望 %v", c.k, got, err, c.want)
		}
	}
	rankCases := []struct {
		k     key.Key
		idx   int
		found bool
	}{{1, 0, true}, {5, 3, true}, {9, 6, true}, {0, 0, false}, {4, 3, false}, {100, 7, false}}
	for _, c := range rankCases {
		idx, found := r.RankOf(c.k)
		if idx != c.idx || found != c.found {
			t.Errorf("RankOf(%d) = %d,%v 期望 %d,%v", c.k, idx, found, c.idx, c.found)
		}
	}
	rangeCases := []struct {
		lo, hi key.Key
		want   int
	}{{1, 9, 7}, {1, 1, 2}, {5, 5, 2}, {2, 6, 3}, {0, 100, 7}, {8, 100, 1}, {4, 4, 0}}
	for _, c := range rangeCases {
		got, err := r.Range(c.lo, c.hi)
		if err != nil || got != c.want {
			t.Errorf("Range(%d,%d) = %d,%v 期望 %d", c.lo, c.hi, got, err, c.want)
		}
	}
}

func TestErrors(t *testing.T) {
	l := buildList(t, []int{10, 20, 30})
	r := New(l)
	for _, k := range []int{-1, -100, 3, 4, 1 << 20} {
		if _, err := r.At(k); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("At(%d) 期望 ErrOutOfRange，得到 %v", k, err)
		}
	}
	if _, err := r.Range(30, 10); !errors.Is(err, ErrBadRange) {
		t.Errorf("Range(30,10) 期望 ErrBadRange，得到 %v", err)
	}
	if l.Size() != 3 {
		t.Error("错误调用不得改变结构")
	}
}

func TestVisitedSublinear(t *testing.T) {
	for _, n := range []int{1000, 100000} {
		l := list.New(32, 1<<30)
		for v := 0; v < n; v++ {
			if err := l.Insert(key.Key(v)); err != nil {
				t.Fatal(err)
			}
		}
		r := New(l)
		if _, err := r.At(n / 2); err != nil {
			t.Fatal(err)
		}
		if v := r.visited.Load(); v > 80 {
			t.Errorf("n=%d At 访问 %d 个节点，超过上限 80", n, v)
		}
		if _, found := r.RankOf(key.Key(n / 2)); !found {
			t.Fatal("RankOf 未命中存在的键")
		}
		if v := r.visited.Load(); v > 80 {
			t.Errorf("n=%d RankOf 访问 %d 个节点，超过上限 80", n, v)
		}
	}
}
