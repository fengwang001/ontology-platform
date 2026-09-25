// Package heap 维护 SpaceSaving 的计数器最小堆：按 (Count, Key) 升序定位
// count 最小的计数器，并列时 Key 小者在前。不依赖其他包。
package heap

// Counter 是一个监视位：(键, 估计计数, 误差)。
type Counter struct {
	Key   int
	Count int
	Err   int
}

// Heap 是按 (Count, Key) 升序的最小堆。
// findCmp 记录最近一次替换中“为找到最小 count 计数器”所做的比较次数，
// 非导出，不出现在任何公开接口里。
type Heap struct {
	items   []Counter
	pos     map[int]int // Key -> items 下标
	findCmp int
}

// New 返回一个空堆。
func New() *Heap { return &Heap{pos: make(map[int]int)} }

// Len 返回计数器个数。
func (h *Heap) Len() int { return len(h.items) }

// less 定义堆序：先比 Count，并列比 Key。
func (h *Heap) less(i, j int) bool {
	a, b := h.items[i], h.items[j]
	return a.Count < b.Count || (a.Count == b.Count && a.Key < b.Key)
}

func (h *Heap) swap(i, j int) {
	h.items[i], h.items[j] = h.items[j], h.items[i]
	h.pos[h.items[i].Key] = i
	h.pos[h.items[j].Key] = j
}

// Add 推入一个新计数器（调用方保证 Key 不存在）。
func (h *Heap) Add(c Counter) {
	h.items = append(h.items, c)
	h.pos[c.Key] = len(h.items) - 1
	h.up(len(h.items) - 1)
}

// Inc 将 Key 的计数加一（调用方保证 Key 存在）。
func (h *Heap) Inc(key int) {
	i := h.pos[key]
	h.items[i].Count++
	h.down(i)
}

// Min 返回当前 (Count, Key) 最小的计数器，即替换候选。
// 找最小只读堆顶，比较次数恒为 1，与堆大小无关。
func (h *Heap) Min() Counter {
	h.findCmp = 1 // 只考察堆顶一个元素
	return h.items[0]
}

// ReplaceMin 用 c 替换当前最小计数器，返回被替换者。
// 调用方应先读 Min 并按规则填好 c 的 Count/Err。
func (h *Heap) ReplaceMin(c Counter) Counter {
	old := h.items[0]
	delete(h.pos, old.Key)
	h.items[0] = c
	h.pos[c.Key] = 0
	h.down(0)
	return old
}

// Get 返回 Key 的计数器与是否存在。
func (h *Heap) Get(key int) (Counter, bool) {
	i, ok := h.pos[key]
	if !ok {
		return Counter{}, false
	}
	return h.items[i], true
}

// Items 返回全部计数器的副本（顺序未定）。
func (h *Heap) Items() []Counter {
	out := make([]Counter, len(h.items))
	copy(out, h.items)
	return out
}

func (h *Heap) up(i int) {
	for i > 0 {
		p := (i - 1) / 2
		if !h.less(i, p) {
			break
		}
		h.swap(i, p)
		i = p
	}
}

func (h *Heap) down(i int) {
	for {
		l, r, m := 2*i+1, 2*i+2, i
		if l < len(h.items) && h.less(l, m) {
			m = l
		}
		if r < len(h.items) && h.less(r, m) {
			m = r
		}
		if m == i {
			return
		}
		h.swap(i, m)
		i = m
	}
}
