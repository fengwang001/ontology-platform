// Package tablespace 实现带区段状态与段碎片页的表空间页分配器。
package tablespace

import (
	"errors"
	"sort"
	"sync"
)

// 区段状态。
const (
	StateFree     = "FREE"
	StateFrag     = "FRAG"
	StateFullFrag = "FULLFRAG"
	StateSeg      = "SEG"
)

// 拒绝原因，按固定优先级区分。
var (
	ErrInvalidArgument = errors.New("tablespace: invalid argument")
	ErrNoSuchSegment   = errors.New("tablespace: segment does not exist")
	ErrPageNotOwned    = errors.New("tablespace: page is not owned by the segment")
	ErrNoSpace         = errors.New("tablespace: no space available")
)

// ExtentState 描述一个区段的可观测状态。
type ExtentState struct {
	Index int    // 区段编号
	State string // FREE / FRAG / FULLFRAG / SEG
	Owner int    // SEG 状态下的独占段号；其余状态为 0
	Used  int    // 已用页数
}

// segMeta 记录单个段的记账信息。
type segMeta struct {
	alive bool
	used  int

	// 独占区段（SEG(s)）链表，按加入次序；head 为非满队列队首。
	headExt int
	tailExt int

	// 碎片页：page -> fragPages 中的下标，用于 O(1) 删除。
	fragPages []int
	fragPos   map[int]int
}

// extent 记录单个区段的归属与队列指针。
type extent struct {
	state byte // 0 FREE, 1 FRAG, 2 FULLFRAG, 3 SEG
	owner int
	used  int

	// 段独占区段链表的前后指针，-1 表示无。
	prev int
	next int

	// 每页归属段号；FRAG/FULLFRAG 时惰性分配，SEG 时可直接用 owner。
	pageOwner []int32
}

// Allocator 是并发安全的表空间页分配器。
type Allocator struct {
	mu sync.Mutex

	x int // 每区段页数
	f int // 碎片阈值
	e int // 区段数

	extents []extent
	segs    []*segMeta // 下标 0 占位，段号从 1 起

	freeHeap []int // 编号最小的 FREE 区段（惰性条目）
	fragHeap []int // 编号最小的 FRAG 区段（惰性条目）
}

// New 构造分配器：X 为每区段页数（2..1024），F 为碎片阈值（1..X），E 为区段数（1..1e5）。
func New(x, f, e int) (*Allocator, error) {
	if x < 2 || x > 1024 || f < 1 || f > x || e < 1 || e > 100000 {
		return nil, ErrInvalidArgument
	}
	a := &Allocator{x: x, f: f, e: e}
	a.extents = make([]extent, e)
	for i := range a.extents {
		a.extents[i].prev = -1
		a.extents[i].next = -1
	}
	a.segs = []*segMeta{{}} // 下标 0 占位
	for i := 0; i < e; i++ {
		a.freeHeap = append(a.freeHeap, i)
	}
	return a, nil
}

// NewSegment 创建新段，返回从 1 起递增、不复用的段号。
func (a *Allocator) NewSegment() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	seg := &segMeta{alive: true, headExt: -1, tailExt: -1, fragPos: map[int]int{}}
	a.segs = append(a.segs, seg)
	return len(a.segs) - 1
}

// AllocPage 为段 s 分配一页，返回页号；失败时返回 -1 与对应原因错误。
func (a *Allocator) AllocPage(s int, hint int) (int, error) {
	if hint < -1 || hint >= a.e*a.x {
		return -1, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s <= 0 || s >= len(a.segs) || !a.segs[s].alive {
		return -1, ErrNoSuchSegment
	}
	seg := a.segs[s]
	if seg.used < a.f {
		return a.allocFragLocked(s, seg)
	}
	return a.allocSegLocked(s, seg, hint)
}

// FreePage 释放段 s 拥有的已分配页 p。
func (a *Allocator) FreePage(s, p int) error {
	if p < 0 || p >= a.e*a.x {
		return ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s <= 0 || s >= len(a.segs) || !a.segs[s].alive {
		return ErrNoSuchSegment
	}
	idx := p / a.x
	off := p % a.x
	ex := &a.extents[idx]
	switch ex.state {
	case 1, 2: // FRAG / FULLFRAG
		if ex.pageOwner[off] != int32(s) {
			return ErrPageNotOwned
		}
	case 3: // SEG
		if ex.owner != s {
			return ErrPageNotOwned
		}
		// 独占区段内的空闲页并不归该段所有，必须确认该页已分配。
		if ex.pageOwner != nil {
			if ex.pageOwner[off] == 0 {
				return ErrPageNotOwned
			}
		} else if off >= ex.used { // 紧凑前缀：仅 [0,used) 已分配
			return ErrPageNotOwned
		}
	default:
		return ErrPageNotOwned
	}
	seg := a.segs[s]
	seg.used--

	if ex.state == 1 || ex.state == 2 {
		ex.pageOwner[off] = 0
		ex.used--
		a.removeFragPageLocked(seg, p)
		if ex.state == 2 {
			ex.state = 1 // FULLFRAG -> FRAG
			a.fragHeapPushLocked(idx)
		}
		if ex.used == 0 {
			ex.state = 0 // FRAG -> FREE
			ex.pageOwner = nil
			a.freeHeapPushLocked(idx)
		}
		return nil
	}

	// SEG(s)
	wasFull := ex.used == a.x
	if ex.pageOwner == nil && off != ex.used-1 {
		// 紧凑前缀 [0,used) 释放非末页会留下空洞，物化为归属数组。
		// 此时 ex.used 尚未递减，末页下标恰为 ex.used-1。
		owners := make([]int32, a.x)
		for k := 0; k < ex.used; k++ {
			owners[k] = int32(s)
		}
		ex.pageOwner = owners
	}
	if ex.pageOwner != nil {
		ex.pageOwner[off] = 0
	}
	ex.used--
	if wasFull {
		a.enqueueExtentLocked(s, seg, idx) // 满区段腾出空位，排到队尾
	}
	if ex.used == 0 {
		a.dequeueExtentLocked(seg, idx)
		ex.state = 0 // SEG(s) -> FREE
		ex.owner = 0
		ex.pageOwner = nil
		a.freeHeapPushLocked(idx)
	}
	return nil
}

// FreeSegment 释放段 s 的全部页与区段并使段号失效。
func (a *Allocator) FreeSegment(s int) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s <= 0 || s >= len(a.segs) || !a.segs[s].alive {
		return ErrNoSuchSegment
	}
	seg := a.segs[s]

	// 先释放全部碎片页（按区段、区段内页号升序，保证迁移顺序可复现）。
	fragByExt := make(map[int][]int)
	var extOrder []int
	for _, page := range seg.fragPages {
		idx := page / a.x
		if _, ok := fragByExt[idx]; !ok {
			extOrder = append(extOrder, idx)
		}
		fragByExt[idx] = append(fragByExt[idx], page)
	}
	sortInts(extOrder)
	for _, idx := range extOrder {
		pages := fragByExt[idx]
		sortInts(pages)
		ex := &a.extents[idx]
		for _, page := range pages {
			ex.pageOwner[page%a.x] = 0
			ex.used--
		}
		if ex.state == 2 {
			ex.state = 1 // FULLFRAG -> FRAG
			a.fragHeapPushLocked(idx)
		}
		if ex.used == 0 {
			ex.state = 0 // -> FREE
			ex.pageOwner = nil
			a.freeHeapPushLocked(idx)
		}
	}

	// 再释放独占区段。满区段已不在队列中，故扫描全部 SEG(s) 区段；
	// reset 会清空链表指针，不能边遍历链表边 reset。
	for idx := range a.extents {
		ex := &a.extents[idx]
		if ex.state == 3 && ex.owner == s {
			a.resetOwnedExtentLocked(idx)
			a.freeHeapPushLocked(idx)
		}
	}

	seg.alive = false
	seg.used = 0
	seg.headExt = -1
	seg.tailExt = -1
	seg.fragPages = nil
	seg.fragPos = nil
	return nil
}

// resetOwnedExtentLocked 把独占区段整体归还为 FREE。
func (a *Allocator) resetOwnedExtentLocked(idx int) {
	ex := &a.extents[idx]
	ex.state = 0
	ex.owner = 0
	ex.used = 0
	ex.pageOwner = nil
	ex.prev = -1
	ex.next = -1
}

// Used 返回段 s 拥有的全部页数；段不存在时返回 -1 与 ErrNoSuchSegment。
func (a *Allocator) Used(s int) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s <= 0 || s >= len(a.segs) || !a.segs[s].alive {
		return -1, ErrNoSuchSegment
	}
	return a.segs[s].used, nil
}

// PageOwner 返回页 p 当前归属的段号；空闲页返回 0。页号越界返回 -1 与错误。
func (a *Allocator) PageOwner(p int) (int, error) {
	if p < 0 || p >= a.e*a.x {
		return -1, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	idx := p / a.x
	off := p % a.x
	ex := &a.extents[idx]
	switch ex.state {
	case 1, 2:
		return int(ex.pageOwner[off]), nil
	case 3:
		if ex.pageOwner != nil {
			if ex.pageOwner[off] == 0 {
				return 0, nil
			}
			return ex.owner, nil
		}
		if off >= ex.used { // 紧凑前缀：[0, used) 已用
			return 0, nil
		}
		return ex.owner, nil
	default:
		return 0, nil
	}
}

// ExtentStates 返回全部区段状态的快照（按编号排序）。
func (a *Allocator) ExtentStates() []ExtentState {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]ExtentState, a.e)
	for i := range a.extents {
		ex := &a.extents[i]
		st := ExtentState{Index: i, Used: ex.used}
		switch ex.state {
		case 0:
			st.State = StateFree
		case 1:
			st.State = StateFrag
		case 2:
			st.State = StateFullFrag
		case 3:
			st.State = StateSeg
			st.Owner = ex.owner
		}
		out[i] = st
	}
	return out
}

// ---- 内部实现（调用方必须持有 mu）----

// allocFragLocked 处理 used(s) < F 的碎片分配（忽略 hint）。
func (a *Allocator) allocFragLocked(s int, seg *segMeta) (int, error) {
	idx := a.findFragExtentLocked()
	if idx < 0 {
		idx = a.findFreeExtentLocked()
		if idx < 0 {
			return -1, ErrNoSpace // 不预先转换任何区段
		}
		ex := &a.extents[idx]
		ex.state = 1 // FREE -> FRAG
		ex.pageOwner = make([]int32, a.x)
		a.fragHeapPushLocked(idx)
	}
	ex := &a.extents[idx]
	off := 0
	for off < a.x && ex.pageOwner[off] != 0 {
		off++
	}
	page := idx*a.x + off
	ex.pageOwner[off] = int32(s)
	ex.used++
	seg.used++
	seg.fragPos[page] = len(seg.fragPages)
	seg.fragPages = append(seg.fragPages, page)
	if ex.used == a.x {
		ex.state = 2 // FRAG -> FULLFRAG
	}
	return page, nil
}

// allocSegLocked 处理 used(s) >= F 的整区分配。
func (a *Allocator) allocSegLocked(s int, seg *segMeta, hint int) (int, error) {
	// hint 落在 s 已独占区段且该页空闲时，分配 hint 页。
	if hint >= 0 {
		hidx := hint / a.x
		hoff := hint % a.x
		if a.extents[hidx].state == 3 && a.extents[hidx].owner == s &&
			a.isSegPageFreeLocked(&a.extents[hidx], hoff) {
			a.assignSegPageLocked(s, seg, hidx, hoff)
			return hint, nil
		}
	}
	// 否则取非满队列队首区段的最小空闲页。
	if seg.headExt != -1 {
		idx := seg.headExt
		ex := &a.extents[idx]
		off := 0
		for off < a.x && !a.isSegPageFreeLocked(ex, off) {
			off++
		}
		a.assignSegPageLocked(s, seg, idx, off)
		return idx*a.x + off, nil
	}
	// 队列为空：取编号最小的 FREE 区段转 SEG(s)，排到队尾（不看 hint）。
	idx := a.findFreeExtentLocked()
	if idx < 0 {
		return -1, ErrNoSpace
	}
	ex := &a.extents[idx]
	ex.state = 3
	a.enqueueExtentLocked(s, seg, idx)
	a.assignSegPageLocked(s, seg, idx, 0)
	return idx * a.x, nil
}

// isSegPageFreeLocked 判断 SEG 区段内某页是否空闲。
func (a *Allocator) isSegPageFreeLocked(ex *extent, off int) bool {
	if ex.pageOwner != nil {
		return ex.pageOwner[off] == 0
	}
	return off >= ex.used // 紧凑前缀：已用页为 [0, used)
}

// assignSegPageLocked 在独占区段内分配一页，并在用满时移出队列。
func (a *Allocator) assignSegPageLocked(s int, seg *segMeta, idx, off int) {
	ex := &a.extents[idx]
	if ex.pageOwner == nil && off != ex.used {
		// 紧凑前缀被稀疏分配（hint 跳页），物化为归属数组。
		owners := make([]int32, a.x)
		for k := 0; k < ex.used; k++ {
			owners[k] = int32(s)
		}
		ex.pageOwner = owners
	}
	if ex.pageOwner != nil {
		ex.pageOwner[off] = int32(s)
	}
	ex.used++
	seg.used++
	if ex.used == a.x {
		a.dequeueExtentLocked(seg, idx) // 用满移出非满队列
	}
}

// findFreeExtentLocked 返回编号最小的 FREE 区段。
// 堆顶区段在被取用并转换状态后才弹出条目。
func (a *Allocator) findFreeExtentLocked() int {
	for len(a.freeHeap) > 0 {
		idx := a.freeHeap[0]
		if a.extents[idx].state == 0 {
			return idx
		}
		heapPop(&a.freeHeap)
	}
	return -1
}

// findFragExtentLocked 返回编号最小的、当前确为 FRAG 的区段。
// 堆顶区段在被取用并转换状态后才弹出条目（转 FULLFRAG/FREE 后惰性丢弃）。
func (a *Allocator) findFragExtentLocked() int {
	for len(a.fragHeap) > 0 {
		idx := a.fragHeap[0]
		if a.extents[idx].state == 1 {
			return idx
		}
		heapPop(&a.fragHeap)
	}
	return -1
}

func (a *Allocator) fragHeapPushLocked(idx int) { heapPush(&a.fragHeap, idx) }

func (a *Allocator) freeHeapPushLocked(idx int) { heapPush(&a.freeHeap, idx) }

// enqueueExtentLocked 把独占区段排到 s 的非满队列队尾。
func (a *Allocator) enqueueExtentLocked(s int, seg *segMeta, idx int) {
	ex := &a.extents[idx]
	ex.owner = s
	ex.prev = seg.tailExt
	ex.next = -1
	if seg.tailExt != -1 {
		a.extents[seg.tailExt].next = idx
	} else {
		seg.headExt = idx
	}
	seg.tailExt = idx
}

// dequeueExtentLocked 从 s 的非满队列中移除指定区段（不改变其 SEG 状态）。
func (a *Allocator) dequeueExtentLocked(seg *segMeta, idx int) {
	ex := &a.extents[idx]
	if ex.prev != -1 {
		a.extents[ex.prev].next = ex.next
	} else if seg.headExt == idx {
		seg.headExt = ex.next
	}
	if ex.next != -1 {
		a.extents[ex.next].prev = ex.prev
	} else if seg.tailExt == idx {
		seg.tailExt = ex.prev
	}
	ex.prev = -1
	ex.next = -1
}

// removeFragPageLocked 从段的碎片页集合中删除一页。
func (a *Allocator) removeFragPageLocked(seg *segMeta, page int) {
	pos := seg.fragPos[page]
	last := len(seg.fragPages) - 1
	if pos != last {
		seg.fragPages[pos] = seg.fragPages[last]
		seg.fragPos[seg.fragPages[pos]] = pos
	}
	seg.fragPages = seg.fragPages[:last]
	delete(seg.fragPos, page)
}

// ---- 编号最小堆（小顶堆，元素为区段编号，允许惰性重复条目）----

func heapPush(h *[]int, v int) {
	*h = append(*h, v)
	i := len(*h) - 1
	for i > 0 {
		parent := (i - 1) / 2
		if (*h)[parent] <= (*h)[i] {
			break
		}
		(*h)[parent], (*h)[i] = (*h)[i], (*h)[parent]
		i = parent
	}
}

func heapPop(h *[]int) int {
	top := (*h)[0]
	n := len(*h) - 1
	(*h)[0] = (*h)[n]
	*h = (*h)[:n]
	for i := 0; ; {
		left := 2*i + 1
		right := left + 1
		smallest := i
		if left < len(*h) && (*h)[left] < (*h)[smallest] {
			smallest = left
		}
		if right < len(*h) && (*h)[right] < (*h)[smallest] {
			smallest = right
		}
		if smallest == i {
			break
		}
		(*h)[i], (*h)[smallest] = (*h)[smallest], (*h)[i]
		i = smallest
	}
	return top
}

func sortInts(v []int) { sort.Ints(v) }
