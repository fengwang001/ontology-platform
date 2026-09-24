// Package chunk 实现 chunk 生命周期与水位修正、Poll 过滤应用与下游视图，只依赖 wal。
package chunk

import (
	"errors"
	"maps"
	"ontology/wal"
	"slices"
	"sort"
	"sync"
)

// Out 是一条下游变更：Upsert=+(Key,Val)，Delete=-(Key)。
type Out struct {
	Op  wal.Op
	Key int64
	Val string
}

var ErrInvalidRange = errors.New("chunk: lo must be less than hi")
var ErrOverlapping = errors.New("chunk: range overlaps an existing chunk")
var ErrStage = errors.New("chunk: invalid lifecycle stage order")
var ErrViewTooLarge = errors.New("chunk: view row count would exceed maxRows")

type region struct{ lo, hi, h int64 }
type activeChunk struct {
	lo, hi int64
	l      int
	read   bool
	snap   map[int64]string
}

type Coordinator struct {
	lg                   *wal.Log
	maxRows              int
	mu                   sync.Mutex
	done                 []region
	active               *activeChunk
	view                 map[int64]string
	pollPos, lastChecked int
}

func New(lg *wal.Log, maxRows int) *Coordinator {
	return &Coordinator{lg: lg, maxRows: maxRows, view: map[int64]string{}}
}

func (c *Coordinator) BeginChunk(lo, hi int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lo >= hi {
		return ErrInvalidRange
	}
	i := sort.Search(len(c.done), func(i int) bool { return c.done[i].hi > lo })
	if i < len(c.done) && c.done[i].lo < hi {
		return ErrOverlapping
	}
	if c.active != nil {
		if lo < c.active.hi && c.active.lo < hi { // 与进行中 chunk 相交
			return ErrOverlapping
		}
		return ErrStage
	}
	c.active = &activeChunk{lo: lo, hi: hi, l: c.lg.Position()}
	return nil
}

func (c *Coordinator) ReadChunk() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil || c.active.read {
		return ErrStage
	}
	c.active.snap = c.lg.Snapshot(c.active.lo, c.active.hi)
	c.active.read = true
	return nil
}

func (c *Coordinator) EndChunk() ([]Out, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil || !c.active.read {
		return nil, ErrStage
	}
	a, h := c.active, c.lg.Position()
	ents := c.lg.Slice(a.l, h) // 下标直接定位 (L,H]，不从头扫描整条日志
	c.lastChecked = len(ents)
	final := maps.Clone(a.snap)
	for _, e := range ents {
		if e.Key < a.lo || e.Key >= a.hi {
			continue
		}
		if e.Op == wal.Upsert {
			final[e.Key] = e.Val
		} else {
			delete(final, e.Key)
		}
	}
	if len(c.view)+len(final) > c.maxRows {
		return nil, ErrViewTooLarge
	}
	maps.Copy(c.view, final)
	c.done = append(c.done, region{a.lo, a.hi, int64(h)})
	sort.Slice(c.done, func(i, j int) bool { return c.done[i].lo < c.done[j].lo })
	c.active = nil
	out := make([]Out, 0, len(final))
	for _, k := range slices.Sorted(maps.Keys(final)) {
		out = append(out, Out{wal.Upsert, k, final[k]})
	}
	return out, nil
}

func (c *Coordinator) owner(k int64) (region, bool) {
	i := sort.Search(len(c.done), func(i int) bool { return c.done[i].hi > k })
	if i < len(c.done) && k >= c.done[i].lo {
		return c.done[i], true
	}
	return region{}, false
}

func (c *Coordinator) Poll() ([]Out, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur := c.lg.Position()
	sim := maps.Clone(c.view)
	var out []Out
	for i, e := range c.lg.Slice(c.pollPos, cur) {
		r, ok := c.owner(e.Key)
		if !ok || int64(c.pollPos+i+1) <= r.h {
			continue
		}
		if e.Op == wal.Upsert {
			sim[e.Key] = e.Val
			out = append(out, Out{wal.Upsert, e.Key, e.Val})
		} else if _, ok := sim[e.Key]; ok {
			delete(sim, e.Key)
			out = append(out, Out{wal.Delete, e.Key, ""})
		}
	}
	if len(sim) > c.maxRows {
		return nil, ErrViewTooLarge
	}
	c.view, c.pollPos = sim, cur
	return out, nil
}

func (c *Coordinator) View() map[int64]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return maps.Clone(c.view)
}
