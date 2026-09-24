// Package topk incrementally maintains TopK over a wtop window: two heaps
// (members / candidates) swap only when ranking requires it.
package topk

import (
	"container/heap"
	"errors"
	"reflect"
	"sort"
	"strconv"
	"sync/atomic"

	"ontology/wtop"
)

// Entry is one ranked key; Change re-exports a wtop event through this layer.
type Entry struct {
	Key string
	Sum int64
}
type Change = wtop.Change

var ErrSelfCheck = errors.New("topk: self-check failed")

type item struct {
	key string
	sum int64
	idx int
	cur *bucket // heap currently holding this item (m.in or m.out)
}
type bucket struct {
	xs   []*item
	less func(a, b *item) bool
}

func (b *bucket) Len() int           { return len(b.xs) }
func (b *bucket) Less(i, j int) bool { return b.less(b.xs[i], b.xs[j]) }
func (b *bucket) Swap(i, j int)      { b.xs[i], b.xs[j] = b.xs[j], b.xs[i]; b.xs[i].idx, b.xs[j].idx = i, j }
func (b *bucket) Push(x any)         { it := x.(*item); it.idx = len(b.xs); b.xs = append(b.xs, it) }
func (b *bucket) Pop() any           { x := b.xs[len(b.xs)-1]; b.xs = b.xs[:len(b.xs)-1]; return x }

// Model: in=members (worst root), out=candidates (best root); pops is the unexported count of the last query's heap pops.
type Model struct {
	win     *wtop.Window
	k       int
	all     map[string]*item
	in, out *bucket
	pops    atomic.Int64
}

func New(k, n int) *Model {
	return &Model{win: wtop.New(n), k: k, all: map[string]*item{}, in: &bucket{less: worse}, out: &bucket{less: better}}
}

// better ranks a ahead of b (larger sum, ties key asc); worse inverts it.
func better(a, b *item) bool { return a.sum > b.sum || (a.sum == b.sum && a.key < b.key) }
func worse(a, b *item) bool  { return a.sum < b.sum || (a.sum == b.sum && a.key > b.key) }

// Apply incrementally repairs TopK after one change: only entering/evicted keys move, then a strictly better candidate swaps with the worst member.
func (m *Model) Apply(c wtop.Change) {
	old, evicted := m.win.Add(c)
	m.resync(c.Key)
	if evicted {
		m.resync(old.Key)
	}
	for m.in.Len() < m.k && m.out.Len() > 0 {
		it := heap.Pop(m.out).(*item)
		it.cur = m.in
		heap.Push(m.in, it)
	}
	for m.in.Len() > 0 && m.out.Len() > 0 && better(m.out.xs[0], m.in.xs[0]) {
		a, b := heap.Pop(m.out).(*item), heap.Pop(m.in).(*item)
		a.cur, b.cur = m.in, m.out
		heap.Push(m.in, a)
		heap.Push(m.out, b)
	}
}

// resync repositions one affected key from its window sum (dropping it once it has left the window), parking it among candidates.
func (m *Model) resync(key string) {
	sum, exists := m.win.Sum(key)
	it, known := m.all[key]
	if known {
		heap.Remove(it.cur, it.idx)
	}
	if !exists {
		if known {
			delete(m.all, key)
		}
		return
	}
	if known {
		it.sum = sum
	} else {
		it = &item{key: key, sum: sum}
		m.all[key] = it
	}
	it.cur = m.out
	heap.Push(m.out, it)
}

// TopK returns up to K entries (sum desc, ties key asc) from a private snapshot heap; shared state is untouched (RLock safe).
func (m *Model) TopK() []Entry {
	snap := bucket{less: worse}
	for _, x := range m.in.xs {
		c := *x
		snap.xs = append(snap.xs, &c)
	}
	m.pops.Store(0)
	res := make([]Entry, snap.Len())
	for i := len(res) - 1; i >= 0; i-- { // pop worst-first, fill from the end
		m.pops.Add(1)
		x := heap.Pop(&snap).(*item)
		res[i] = Entry{x.key, x.sum}
	}
	return res
}

// brute is the naive reference: sum every existing window key and sort.
func brute(win *wtop.Window, k int) []Entry {
	var r []Entry
	win.Range(func(key string, s int64) bool { r = append(r, Entry{key, s}); return true })
	sort.Slice(r, func(i, j int) bool {
		return r[i].Sum > r[j].Sum || (r[i].Sum == r[j].Sum && r[i].Key < r[j].Key)
	})
	return r[:min(len(r), k)]
}

// SelfCheck replays the seven-step sequence against brute at each step and asserts pops stay O(K) for m=100/1000/10000.
func (m *Model) SelfCheck() error {
	seq := []wtop.Change{{Key: "a", Score: 6}, {Key: "a", Score: 4}, {Key: "b", Score: 9}, {Key: "c", Score: 8}, {Key: "d", Score: 7}, {Key: "e", Score: 1}, {Key: "f", Score: 8}}
	mm := New(2, 5)
	for _, e := range seq {
		mm.Apply(e)
		if !reflect.DeepEqual(mm.TopK(), brute(mm.win, 2)) {
			return ErrSelfCheck
		}
	}
	for _, n := range []int{100, 1000, 10000} {
		m2 := New(10, n)
		for i := 0; i < n; i++ {
			m2.Apply(wtop.Change{Key: strconv.Itoa(i), Score: int64(i)*7 - 3})
		}
		m2.TopK()
		if m2.pops.Load() > int64(10+4) {
			return ErrSelfCheck
		}
	}
	return nil
}
