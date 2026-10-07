package scheduler

import "container/heap"

// groupHeap 是按 (累计被限时长, 组编号) 排序的小顶堆，支撑选组：
// 每个时段的选组开销只与组数相关，与用户数无关。
type groupHeap struct {
	acc   map[string]int64 // 组编号 -> 累计被限时长（堆的唯一权威存储）
	ids   []string
	index map[string]int
	ops   *int64 // 可选的开销计数器（模拟用堆传 nil）
}

func newGroupHeap(acc map[string]int64, ops *int64) *groupHeap {
	h := &groupHeap{acc: acc, index: make(map[string]int, len(acc)), ops: ops}
	for id := range acc {
		h.ids = append(h.ids, id)
	}
	heap.Init(h)
	return h
}

func (h *groupHeap) Len() int { return len(h.ids) }

func (h *groupHeap) Less(i, j int) bool {
	a, b := h.ids[i], h.ids[j]
	if h.acc[a] != h.acc[b] {
		return h.acc[a] < h.acc[b]
	}
	return a < b // 累计相同，组编号小者优先
}

func (h *groupHeap) Swap(i, j int) {
	h.ids[i], h.ids[j] = h.ids[j], h.ids[i]
	h.index[h.ids[i]] = i
	h.index[h.ids[j]] = j
}

func (h *groupHeap) Push(x any) {
	id := x.(string)
	h.index[id] = len(h.ids)
	h.ids = append(h.ids, id)
}

func (h *groupHeap) Pop() any {
	old := h.ids
	n := len(old)
	id := old[n-1]
	h.ids = old[:n-1]
	delete(h.index, id)
	return id
}

func (h *groupHeap) bump() {
	if h.ops != nil {
		*h.ops++
	}
}

// add 给组的累计时长加 delta 并修复堆（时段结束时结算用）。
func (h *groupHeap) add(id string, delta int64) {
	h.acc[id] += delta
	h.bump()
	heap.Fix(h, h.index[id])
}

// insert 把新组放入堆（acc[id] 须已设置）。
func (h *groupHeap) insert(id string) {
	h.bump()
	heap.Push(h, id)
}

// popK 弹出累计最短的 k 个组后再原样放回（累计不变），返回弹出顺序即选中顺序。
func (h *groupHeap) popK(k int) []string {
	if k <= 0 {
		return nil
	}
	sel := make([]string, 0, k)
	for i := 0; i < k; i++ {
		h.bump()
		sel = append(sel, heap.Pop(h).(string))
	}
	for _, id := range sel {
		h.bump()
		heap.Push(h, id)
	}
	return sel
}

// selectAndCredit 弹出累计最短的 k 个组、各加 delta 后放回（预测模拟用）。
func (h *groupHeap) selectAndCredit(k int, delta int64) []string {
	if k <= 0 {
		return nil
	}
	sel := make([]string, 0, k)
	for i := 0; i < k; i++ {
		h.bump()
		sel = append(sel, heap.Pop(h).(string))
	}
	for _, id := range sel {
		h.acc[id] += delta
		h.bump()
		heap.Push(h, id)
	}
	return sel
}

// lvlEntry 是有效等级堆的元素；惰性删除：弹出时与 live 表核对。
type lvlEntry struct {
	level int
	id    string
}

// levelHeap 是活动指令等级的大顶堆。已取消/已结束的指令只在堆顶被惰性弹出一次，
// 因此判定某时段有效等级的开销不随已取消或已结束指令的数量增长（均摊 O(log n)）。
type levelHeap struct {
	ents []lvlEntry
	live map[string]int // 指令 -> 当前生效等级（权威表）
	ops  *int64
}

func newLevelHeap(live map[string]int, ops *int64) *levelHeap {
	h := &levelHeap{live: live, ops: ops}
	for id, lv := range live {
		h.ents = append(h.ents, lvlEntry{level: lv, id: id})
	}
	heap.Init(h)
	return h
}

func (h *levelHeap) Len() int           { return len(h.ents) }
func (h *levelHeap) Less(i, j int) bool { return h.ents[i].level > h.ents[j].level }
func (h *levelHeap) Swap(i, j int)      { h.ents[i], h.ents[j] = h.ents[j], h.ents[i] }
func (h *levelHeap) Push(x any)         { h.ents = append(h.ents, x.(lvlEntry)) }
func (h *levelHeap) Pop() any {
	old := h.ents
	n := len(old)
	e := old[n-1]
	h.ents = old[:n-1]
	return e
}

func (h *levelHeap) bump() {
	if h.ops != nil {
		*h.ops++
	}
}

// set 写入指令当前等级并压堆（旧元素成为惰性垃圾）。
func (h *levelHeap) set(id string, level int) {
	h.live[id] = level
	h.bump()
	heap.Push(h, lvlEntry{level: level, id: id})
}

// remove 从权威表删除；堆中残留元素由 top 惰性弹出。
func (h *levelHeap) remove(id string) { delete(h.live, id) }

// top 返回当前有效等级（所有活动指令等级的最大值），无活动指令时为 0。
func (h *levelHeap) top() int {
	for len(h.ents) > 0 {
		e := h.ents[0]
		if lv, ok := h.live[e.id]; ok && lv == e.level {
			return e.level
		}
		h.bump()
		heap.Pop(h)
	}
	return 0
}

// endEntry 用于维护预测视界：指令窗口终点或取消生效边界。
type endEntry struct {
	t      int64
	id     string
	cancel bool
}

// endHeap 是按时间的大顶堆，惰性剔除已被更早取消取代的窗口终点，
// 使预测视界不随已取消/已结束指令数量退化。
type endHeap struct {
	ents   []endEntry
	instrs map[string]*instruction
}

func (h *endHeap) Len() int           { return len(h.ents) }
func (h *endHeap) Less(i, j int) bool { return h.ents[i].t > h.ents[j].t }
func (h *endHeap) Swap(i, j int)      { h.ents[i], h.ents[j] = h.ents[j], h.ents[i] }
func (h *endHeap) Push(x any)         { h.ents = append(h.ents, x.(endEntry)) }
func (h *endHeap) Pop() any {
	old := h.ents
	n := len(old)
	e := old[n-1]
	h.ents = old[:n-1]
	return e
}

func (h *endHeap) push(t int64, id string, cancel bool) {
	heap.Push(h, endEntry{t: t, id: id, cancel: cancel})
}

func (h *endHeap) valid(e endEntry) bool {
	in := h.instrs[e.id]
	if in == nil {
		return false
	}
	if e.cancel {
		return in.cancelled && in.cancelEff == e.t
	}
	// 窗口终点被更早生效的取消取代时失效
	return !in.cancelled || in.cancelEff >= in.end
}

// horizon 返回预测视界：晚于 from 的最远相关边界；没有时返回 from。
func (h *endHeap) horizon(from int64) int64 {
	for len(h.ents) > 0 && !h.valid(h.ents[0]) {
		heap.Pop(h)
	}
	if len(h.ents) > 0 && h.ents[0].t > from {
		return h.ents[0].t
	}
	return from
}
