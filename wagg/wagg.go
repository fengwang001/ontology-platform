// Package wagg 按 (Key, 窗口) 维护计数，随水位线准点触发、迟到更新或丢弃，产出变更日志。
package wagg

import (
	"container/heap"
	"errors"
	"ontology/win"
	"slices"
)

var ErrInvalidParam = errors.New("wagg: size 必须为正，delay/lateness 不可为负")
var ErrEmptyKey = errors.New("wagg: 事件 Key 不能为空串")
var ErrTooManyOpen = errors.New("wagg: 未清除窗口数超过 maxOpen")

type Event struct {
	Key string
	TS  int64
}
type Change struct {
	Key               string
	Start, End, Count int64
	Plus              bool
}
type entry struct {
	key                        string
	start, end, count, emitted int64
}
type entryHeap []*entry

func (h entryHeap) Len() int           { return len(h) }
func (h entryHeap) Less(i, j int) bool { return h[i].end < h[j].end } // 堆只按 end 定位，并列无关正确性
func (h entryHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *entryHeap) Push(x any)        { *h = append(*h, x.(*entry)) }
func (h *entryHeap) Pop() (x any)      { x = (*h)[len(*h)-1]; *h = (*h)[:len(*h)-1]; return x }

// Agg 为单进程内存聚合器；maxOpen<=0 不限。lastChecked 非导出（最近一次推进检查过的窗口数，仅同包白盒测试与探针直读）；entry.emitted>0 即已准点触发。
type Agg struct {
	size, delay, lateness, dropped int64
	maxOpen, open, lastChecked     int
	wm                             win.Watermark
	ents                           map[Event]*entry
	fire, clear                    entryHeap
	log                            []Change
}

func New(size, delay, lateness int64, maxOpen int) *Agg {
	if size <= 0 || delay < 0 || lateness < 0 {
		panic(ErrInvalidParam)
	}
	return &Agg{size: size, delay: delay, lateness: lateness, maxOpen: maxOpen, ents: map[Event]*entry{}}
}

// advance 只看两堆堆顶、不满足即停；lastChecked=触发/清除数+每堆至多一次堆顶探查（≤2），与未清除总数无关。
func (a *Agg) advance(wm int64) []Change {
	n, out := 0, []Change{}
	for len(a.fire) > 0 && a.fire[0].end <= wm {
		n++
		e := heap.Pop(&a.fire).(*entry)
		e.emitted = e.count
		out = append(out, Change{Key: e.key, Start: e.start, End: e.end, Count: e.count, Plus: true})
		heap.Push(&a.clear, e)
	}
	for len(a.clear) > 0 && win.Purge(wm, win.Window{Start: a.clear[0].start, End: a.clear[0].end}, a.lateness) {
		n++
		e := heap.Pop(&a.clear).(*entry)
		delete(a.ents, Event{e.key, e.start})
		a.open--
	}
	a.lastChecked = n + len(a.fire[:min(1, len(a.fire))]) + len(a.clear[:min(1, len(a.clear))])
	return out
}

// admit 不改状态预演整批是否超 maxOpen；清除/丢弃/开窗照规则模拟，正式提交不可能再因超限失败。
func (a *Agg) admit(evs []Event) bool {
	if a.maxOpen <= 0 {
		return true
	}
	wm, n := a.wm, a.open
	set := map[Event]bool{} // 复用 Event：TS 存窗口起点，与 Key 组成窗口身份
	for id := range a.ents {
		set[id] = true
	}
	for _, ev := range evs {
		wmv := wm.Observe(ev.TS, a.delay)
		for id := range set { // 预演允许整表扫描，不在热路径
			if win.DropLate(wmv, win.Assign(id.TS, a.size), a.lateness) {
				delete(set, id)
				n--
			}
		}
		wd := win.Assign(ev.TS, a.size)
		id := Event{ev.Key, wd.Start}
		if win.DropLate(wmv, wd, a.lateness) || set[id] {
			continue
		}
		if n >= a.maxOpen {
			return false
		}
		n, set[id] = n+1, true
	}
	return true
}

func (a *Agg) Feed(evs []Event) (out []Change, err error) {
	if slices.IndexFunc(evs, func(e Event) bool { return e.Key == "" }) >= 0 {
		return nil, ErrEmptyKey
	}
	if !a.admit(evs) {
		return nil, ErrTooManyOpen
	}
	for _, ev := range evs {
		wm := a.wm.Observe(ev.TS, a.delay) // 丢弃与接受事件同样先推进水位线
		out = append(out, a.advance(wm)...)
		wd := win.Assign(ev.TS, a.size)
		if win.DropLate(wm, wd, a.lateness) {
			a.dropped++
			continue
		}
		id := Event{ev.Key, wd.Start}
		e := a.ents[id]
		if e == nil {
			e = &entry{key: ev.Key, start: wd.Start, end: wd.End}
			a.ents[id], a.open = e, a.open+1
			heap.Push(&a.fire, e)
		}
		e.count++
		if e.emitted != 0 { // 迟到且在 lateness 内：先撤旧值再发新值
			out = append(out, Change{Key: e.key, Start: e.start, End: e.end, Count: e.emitted, Plus: false}, Change{Key: e.key, Start: e.start, End: e.end, Count: e.count, Plus: true})
			e.emitted = e.count
		}
	}
	a.log = append(a.log, out...)
	return out, nil
}

func (a *Agg) Flush() []Change { // 水位线推进到正无穷，触发并清除全部残留窗口
	out := a.advance(a.wm.Advance(win.PosInf))
	a.log = append(a.log, out...)
	return out
}
func (a *Agg) Log() []Change  { cp := make([]Change, len(a.log)); copy(cp, a.log); return cp }
func (a *Agg) Dropped() int64 { return a.dropped }

//go:noinline
func lastCheckedOf(a *Agg) int { return a.lastChecked } // 非导出：仅供白盒测试与 demo 的 linkname 读取，数值不经导出 API
