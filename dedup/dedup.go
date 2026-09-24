// Package dedup 维护按 ID 去重记忆，按过期点有序清除并计数；依赖 dwin。
package dedup

import (
	"container/heap"
	"errors"
	"fmt"

	"ontology/dwin"
)

type Event struct {
	ID string
	TS int64
}

var (
	ErrInvalidParam = errors.New("dedup: ttl/maxIDs 必须为正，delay 不得为负")
	ErrEmptyID      = errors.New("dedup: 事件 ID 不能为空")
	ErrTooMany      = errors.New("dedup: 记忆条数超过 maxIDs")
)

// entry 按首见 TS 排序；gen 为插入批次代号，供失败回滚区分批前/批内项；只从堆顶弹出。
type entry struct {
	id      string
	firstTS int64
	gen     int64
}
type byExpiry []*entry

func (h byExpiry) Len() int           { return len(h) }
func (h byExpiry) Less(i, j int) bool { return h[i].firstTS < h[j].firstTS }
func (h byExpiry) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *byExpiry) Push(x any)        { *h = append(*h, x.(*entry)) }
func (h *byExpiry) Pop() any          { e := (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return e }

type Table struct {
	ttl    int64
	maxIDs int
	wm     *dwin.Watermark
	mem    map[string]*entry
	pq     byExpiry
	dups   int64
	probes int   // 非导出：最近一次水位线推进时检查过是否过期的记忆条数
	gen    int64 // 单调递增的批次代号
}

func NewTable(ttl, delay int64, maxIDs int) (*Table, error) {
	if ttl <= 0 || delay < 0 || maxIDs <= 0 {
		return nil, ErrInvalidParam
	}
	return &Table{ttl: ttl, maxIDs: maxIDs, wm: dwin.New(delay), mem: map[string]*entry{}}, nil
}

// sweep 清除过期记忆，仅水位线推进时调用；堆顶最早过期，探测数 = 存活堆顶（≤1）+ 清除数。被清批前项记入 restore。
func (t *Table) sweep(wm int64, valid bool, gen int64, restore *[]entry) {
	t.probes = 0
	for t.pq.Len() > 0 {
		t.probes++
		top := t.pq[0]
		if !dwin.Expired(wm, valid, top.firstTS, t.ttl) {
			return
		}
		heap.Pop(&t.pq)
		delete(t.mem, top.id)
		if top.gen != gen {
			*restore = append(*restore, entry{id: top.id, firstTS: top.firstTS})
		}
	}
}

// Apply 原子处理一批事件；任一条被拒则全部变更整体回滚，成功路径不做整表拷贝。
func (t *Table) Apply(evs []Event) (out []Event, err error) {
	snapMax, snapSeen := t.wm.Snapshot()
	snapDups, gen := t.dups, t.gen+1
	t.gen = gen
	var restore []entry // 本批被 sweep 清掉的批前项
	defer func() {
		if err == nil {
			return
		}
		kept := map[string]*entry{}
		t.pq = nil
		add := func(id string, ts int64) { // 存活批前项与被清项不相交，直接重建
			cp := &entry{id: id, firstTS: ts}
			kept[id] = cp
			heap.Push(&t.pq, cp)
		}
		for _, e := range t.mem {
			if e.gen != gen {
				add(e.id, e.firstTS)
			}
		}
		for i := range restore {
			add(restore[i].id, restore[i].firstTS)
		}
		t.mem, t.dups, out = kept, snapDups, nil
		t.wm.Restore(snapMax, snapSeen)
	}()
	for _, ev := range evs {
		if ev.ID == "" {
			return nil, ErrEmptyID
		}
		wm, valid, adv := t.wm.Observe(ev.TS) // ① 更新水位线
		if adv {
			t.sweep(wm, valid, gen, &restore) // ② 清除过期记忆
		}
		if _, ok := t.mem[ev.ID]; ok { // ③ 存在即重复：丢弃、计数，不刷新首见 TS
			t.dups++
			continue
		}
		e := &entry{id: ev.ID, firstTS: ev.TS, gen: gen} // 新事件记首见 TS
		heap.Push(&t.pq, e)
		t.mem[ev.ID] = e
		if dwin.Expired(wm, valid, ev.TS, t.ttl) { // ④ 记入即过期：其 TS 最小，必在堆顶
			heap.Pop(&t.pq)
			delete(t.mem, ev.ID)
		}
		if len(t.mem) > t.maxIDs {
			return nil, ErrTooMany
		}
		out = append(out, ev)
	}
	return out, nil
}
func (t *Table) Dups() int64              { return t.dups }
func (t *Table) Watermark() (int64, bool) { return t.wm.Get() }
func (t *Table) Mem() map[string]int64 {
	m := map[string]int64{}
	for id, e := range t.mem {
		m[id] = e.firstTS
	}
	return m
}

// CheckProbeBound 验证：m 个不过期记忆后只推进水位线 1、探测恒为 1；导出但只回传成败，计数器数值不进入公开接口。
func CheckProbeBound() error {
	for _, m := range []int{100, 1000, 10000} {
		t, _ := NewTable(10, 2, m+1)
		evs := make([]Event, m)
		for i := range evs {
			evs[i] = Event{ID: fmt.Sprintf("k%d", i), TS: 1_000_000}
		}
		t.Apply(evs)
		if _, e := t.Apply([]Event{{ID: "z", TS: 1_000_001}}); e != nil || t.probes != 1 {
			return errors.New("探测条数随记忆总量线性增长")
		}
	}
	return nil
}
