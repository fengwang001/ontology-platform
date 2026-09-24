// Package dedup 维护按 ID 的去重记忆：每个 ID 只存首见 TS，重复事件不刷新；
// 记忆按过期点（ttl 固定时即首见 TS）有序清除。非并发安全，同步由 api 负责。
package dedup

import (
	"container/heap"
	"errors"
	"strconv"

	"ontology/dwin"
)

type Dedup struct { // scan 为非导出检查条数，仅同包测试白盒读
	ttl  int64
	wm   *dwin.Watermark
	ids  map[string]*item
	h    expiryHeap // 按 firstTS 升序，堆顶即最早过期点
	dups int64
	scan int // 最近一次清除中检查过是否过期的记忆条数
}

// New 以 ttl 与水位线延迟 delay 构造去重记忆。
func New(ttl, delay int64) *Dedup {
	return &Dedup{ttl: ttl, wm: dwin.New(delay), ids: map[string]*item{}}
}

// Process 严格按序：① 更新水位线 ② 清除已过期记忆 ③ 查重（重复不刷新）
// ④ 新事件记入，刚记入即满足过期条件则立即清除。dup=true 表示重复。
func (d *Dedup) Process(id string, ts int64) (dup bool) {
	wm := d.wm.Observe(ts)      // ①
	d.purge(wm)                 // ②
	if _, ok := d.ids[id]; ok { // ③
		d.dups++
		return true
	}
	d.add(id, ts) // ④
	if dwin.Expired(wm, ts, d.ttl) {
		d.remove(id) // 刚记入即过期：立即清除，不留痕
	}
	return false
}

// purge 从最早过期点开始逐项弹出；堆顶未过期则其余必未过期。
func (d *Dedup) purge(wm int64) {
	d.scan = 0
	for d.h.Len() > 0 {
		d.scan++ // 每查看一个堆顶（含终止前最后一次）计一次
		if !dwin.Expired(wm, d.h[0].firstTS, d.ttl) {
			break
		}
		delete(d.ids, heap.Pop(&d.h).(*item).id)
	}
}

func (d *Dedup) Dups() int64 { return d.dups }
func (d *Dedup) Len() int    { return len(d.ids) }
func (d *Dedup) WM() int64   { wm, _ := d.wm.Value(); return wm } // 未见事件时为 dwin.NegInf

// Mem 返回记忆快照（ID -> 首见 TS）。
func (d *Dedup) Mem() map[string]int64 {
	m := make(map[string]int64, len(d.ids))
	for id, it := range d.ids {
		m[id] = it.firstTS
	}
	return m
}

// SelfCheck 白盒核验：多档 m 下再喂一条只让水位线前进 1、不清除任何记忆的
// 新 ID 事件，检查条数必须是与 m 无关的小常数（有界定位而非整表扫描）。
func SelfCheck() error {
	for _, m := range []int{100, 1000, 10000} {
		d := New(1<<60, 2)
		for i := 0; i < m; i++ {
			d.Process("id-"+strconv.Itoa(i), 1_000_000+int64(i))
		}
		d.Process("probe", 1_000_000+int64(m)) // wm 恰前进 1，无记忆过期
		if d.scan > 4 {                        // 堆顶一次未过期即停：应为 1，留余量
			return errScanLinear
		}
	}
	return nil
}

var errScanLinear = errors.New("dedup: expiry scan grew linearly with memory size")

type item struct {
	id      string
	firstTS int64
	idx     int
}

type expiryHeap []*item

func (h expiryHeap) Len() int { return len(h) }
func (h expiryHeap) Less(i, j int) bool {
	return h[i].firstTS < h[j].firstTS || h[i].firstTS == h[j].firstTS && h[i].id < h[j].id
}
func (h expiryHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *expiryHeap) Push(x any) {
	it := x.(*item)
	it.idx = len(*h)
	*h = append(*h, it)
}
func (h *expiryHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

func (d *Dedup) add(id string, ts int64) {
	it := &item{id: id, firstTS: ts}
	heap.Push(&d.h, it)
	d.ids[id] = it
}
func (d *Dedup) remove(id string) { heap.Remove(&d.h, d.ids[id].idx); delete(d.ids, id) }

// State 是不透明状态快照，供 api 层整批失败时回滚。
type State struct {
	maxTS int64
	seen  bool
	ids   map[string]*item
	h     expiryHeap
	dups  int64
}

// Snapshot 深拷贝当前状态。
func (d *Dedup) Snapshot() State {
	ids, h := make(map[string]*item, len(d.h)), make(expiryHeap, len(d.h))
	for i, it := range d.h {
		cp := &item{id: it.id, firstTS: it.firstTS, idx: i}
		h[i], ids[cp.id] = cp, cp
	}
	maxTS, seen := d.wm.Snapshot()
	return State{maxTS, seen, ids, h, d.dups}
}

// Restore 恢复到快照（深拷贝），失败不留痕据此实现。
func (d *Dedup) Restore(s State) {
	ids, h := make(map[string]*item, len(s.h)), make(expiryHeap, len(s.h))
	for i, it := range s.h {
		cp := &item{id: it.id, firstTS: it.firstTS, idx: i}
		h[i], ids[cp.id] = cp, cp
	}
	d.ids, d.h, d.dups = ids, h, s.dups
	d.wm.Restore(s.maxTS, s.seen)
}
