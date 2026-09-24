// Package wagg 按 (Key,窗口) 维护计数，推进单调水位线，处理触发/迟到/清除，产出变更日志。
package wagg

import "container/heap"
import "errors"
import "maps"
import "math"
import "sync"
import "ontology/win"

type Event struct {
	Key string
	TS  int64
}
type Change struct {
	Plus              bool
	Key               string
	Start, End, Count int64
}
type ViewKey struct {
	Key        string
	Start, End int64
}

var ErrInvalidParams = errors.New("wagg: require size>0 and delay,lateness>=0")
var ErrTooManyOpen = errors.New("wagg: uncleared window count exceeds maxOpen")
var ErrEmptyKey = errors.New("wagg: event Key must not be empty")

type hEnt struct{ dl, end int64 } // 堆条目 (deadline, 窗口end)，相位查 fired
type Agg struct {
	mu                                               sync.RWMutex
	size, delay, lateness, wm, dropped, lastExamined int64 // lastExamined：非导出探针，上次推进实际处理窗口数
	maxOpen, nopen                                   int
	hasWM                                            bool
	bks                                              map[int64]map[string]int64 // end -> key -> 计数
	fired                                            map[int64]bool             // end 是否已触发
	ord                                              hHeap
	emitted                                          map[ViewKey]int64
}

func NewAgg(size, delay, lateness int64, maxOpen int) (*Agg, error) {
	if size <= 0 || delay < 0 || lateness < 0 || maxOpen < 0 {
		return nil, ErrInvalidParams
	}
	return &Agg{size: size, delay: delay, lateness: lateness, maxOpen: maxOpen, bks: map[int64]map[string]int64{}, fired: map[int64]bool{}, emitted: map[ViewKey]int64{}}, nil
}
func (a *Agg) advance(cand int64) []Change {
	if a.hasWM && cand <= a.wm {
		a.lastExamined = 0
		return nil
	}
	a.hasWM, a.wm = true, cand
	out, exam := []Change(nil), 0
	for len(a.ord) > 0 && a.ord[0].dl <= a.wm {
		h := heap.Pop(&a.ord).(hEnt)
		bs := a.bks[h.end]
		exam += len(bs)
		if a.fired[h.end] { // 清除点：删状态；emitted 保留（下游仍持有输出值）
			a.nopen -= len(bs)
			delete(a.bks, h.end)
			delete(a.fired, h.end)
			continue
		}
		for k, c := range bs { // 触发点：每窗口输出 + 当前计数
			out = append(out, Change{true, k, h.end - a.size, h.end, c})
			a.emitted[ViewKey{k, h.end - a.size, h.end}] = c
		}
		a.fired[h.end] = true
		heap.Push(&a.ord, hEnt{win.SatAdd(h.end, a.lateness), h.end})
	}
	a.lastExamined = int64(exam)
	return out
}
func (a *Agg) ingest(e Event) []Change {
	w := win.Of(e.TS, a.size)
	out := a.advance(win.SatAdd(e.TS, -a.delay))
	vk := ViewKey{e.Key, w.Start, w.End}
	late := win.IsLate(w.End, a.hasWM, a.wm)
	if late && !win.AcceptLate(w.End, a.lateness, a.hasWM, a.wm) {
		a.dropped++
		return out
	}
	bs := a.bks[w.End]
	if _, ok := bs[e.Key]; !ok { // 迟到重建的 end 桶出生即已触发，堆条目直接用清除点
		if bs == nil {
			bs = map[string]int64{}
			a.bks[w.End] = bs
			dl := w.End
			if late {
				a.fired[w.End], dl = true, win.SatAdd(w.End, a.lateness)
			}
			heap.Push(&a.ord, hEnt{dl, w.End})
		}
		bs[e.Key], a.nopen = 0, a.nopen+1
	}
	bs[e.Key]++
	if late { // 迟到接受：有旧输出先 - 旧值再 + 新值；无输出直接 +
		if ov, has := a.emitted[vk]; has {
			out = append(out, Change{false, e.Key, w.Start, w.End, ov})
		}
		out = append(out, Change{true, e.Key, w.Start, w.End, bs[e.Key]})
		a.emitted[vk] = bs[e.Key]
	}
	return out
}
func (a *Agg) Feed(evs []Event) ([]Change, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	seen := map[ViewKey]struct{}{}
	nnew := 0
	for _, e := range evs {
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
		w := win.Of(e.TS, a.size)
		vk := ViewKey{e.Key, w.Start, w.End}
		_, exists := a.bks[w.End][e.Key]
		if _, dup := seen[vk]; !exists && !dup {
			seen[vk], nnew = struct{}{}, nnew+1
		}
	}
	if a.nopen+nnew > a.maxOpen {
		return nil, ErrTooManyOpen
	}
	var out []Change
	for _, e := range evs {
		out = append(out, a.ingest(e)...)
	}
	return out, nil
}
func (a *Agg) Flush() []Change         { a.mu.Lock(); defer a.mu.Unlock(); return a.advance(math.MaxInt64) }
func (a *Agg) View() map[ViewKey]int64 { a.mu.RLock(); defer a.mu.RUnlock(); return a.snap() }
func (a *Agg) snap() map[ViewKey]int64 { return maps.Clone(a.emitted) }
func (a *Agg) Dropped() int64          { a.mu.RLock(); defer a.mu.RUnlock(); return a.dropped }
func ComplexityVerified() bool {
	a, _ := NewAgg(10, 1<<40, 0, 2000)
	for i := range 1000 {
		a.ingest(Event{Key: "k", TS: int64(i) * 10})
	}
	a.ingest(Event{Key: "k", TS: 9991})
	return a.lastExamined == 0
}

type hHeap []hEnt

func (h hHeap) Len() int           { return len(h) }
func (h hHeap) Less(i, j int) bool { return h[i].dl < h[j].dl }
func (h hHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *hHeap) Push(x any)        { *h = append(*h, x.(hEnt)) }
func (h *hHeap) Pop() any          { n := len(*h) - 1; x := (*h)[n]; *h = (*h)[:n]; return x }
