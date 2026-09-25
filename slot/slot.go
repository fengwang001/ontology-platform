// Package slot 维护 connID 槽状态：每槽 FREE/OPEN 与世代号 gen，
// 用最小堆定位当前空闲的最小 connID。不依赖其他包。
package slot

import (
	"container/heap"
	"errors"
	"math/bits"
)

// ErrNoSlots 表示 Open 时没有任何空闲槽。
var ErrNoSlots = errors.New("slot: no free slot")

// Handle 是一条存活连接的凭据：槽号 + 打开时的世代号。
type Handle struct {
	ID  int
	Gen int
}

// freeHeap 是空闲 connID 的最小堆，堆顶即当前最小空闲槽。
type freeHeap []int

func (h freeHeap) Len() int           { return len(h) }
func (h freeHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h freeHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *freeHeap) Push(x any)        { *h = append(*h, x.(int)) }
func (h *freeHeap) Pop() (v any)      { old := *h; v = old[len(old)-1]; *h = old[:len(old)-1]; return v }

// Table 是 connID 槽表。
type Table struct {
	open []bool   // 每槽是否 OPEN
	gen  []int    // 每槽当前世代号，只在 Open 时递增，Close 不归零
	free freeHeap // 空闲槽最小堆
	// checked 记录最近一次 Open 为定位最小空闲槽而访问堆中槽项的次数
	// （堆顶读取 + 下沉维护）。非导出，公开接口读不到它的数值。
	checked int
}

// New 建一张 C 个槽的表，全部 FREE、gen 全 0。C 必须 ≥1。
func New(C int) *Table {
	if C < 1 {
		panic("slot: C must be >= 1")
	}
	t := &Table{open: make([]bool, C), gen: make([]int, C), free: make(freeHeap, 0, C)}
	for i := 0; i < C; i++ {
		t.free = append(t.free, i)
	}
	return t
}

// Open 分配当前空闲的最小 connID，该槽 gen 加 1，返回句柄（Gen≥1）。
func (t *Table) Open() (Handle, error) {
	t.checked = 0
	if t.free.Len() == 0 {
		return Handle{}, ErrNoSlots
	}
	t.checked++ // 读堆顶即定位到最小空闲槽，无需扫描
	id := heap.Pop(&t.free).(int)
	t.open[id] = true
	t.gen[id]++
	return Handle{ID: id, Gen: t.gen[id]}, nil
}

// Close 要求 h.ID 当前 OPEN 且世代匹配；成功后槽回 FREE（gen 保留）。
// 返回 false 表示句柄无效（由调用方判定具体错误），状态不变。
func (t *Table) Close(h Handle) (freed bool, ok bool) {
	if h.ID < 0 || h.ID >= len(t.open) {
		return false, false
	}
	if !t.open[h.ID] || t.gen[h.ID] != h.Gen {
		return false, false
	}
	t.open[h.ID] = false
	heap.Push(&t.free, h.ID)
	return true, true
}

// State 返回某槽是否 OPEN 及当前世代；id 越界时 ok=false。
func (t *Table) State(id int) (open bool, gen int, ok bool) {
	if id < 0 || id >= len(t.open) {
		return false, 0, false
	}
	return t.open[id], t.gen[id], true
}

// Cap 返回槽数 C。
func (t *Table) Cap() int { return len(t.open) }

// CheckOpenCost 核验最近一次 Open 定位最小空闲槽的代价
// 不超过 ⌈log2(C)⌉+1（堆定位是对数级而非线性扫描）。
// 只返回是否越界，不暴露计数器数值。
func (t *Table) CheckOpenCost() error {
	if t.checked > ceilLog2(t.Cap())+1 {
		return errors.New("slot: open cost exceeds log2(C)+1")
	}
	return nil
}

func ceilLog2(n int) int {
	if n <= 1 {
		return 0
	}
	return bits.Len(uint(n - 1))
}
