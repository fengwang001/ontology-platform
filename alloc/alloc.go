// Package alloc 在 drf 的单任务判定之上做多任务 DRF 分配（主导份额 water-filling）。
// 每任务有楼层 floor（上次份额，首次为 0），水位抬升时楼层处任务汇入活跃集合同步走，
// 任务按楼层放在非导出最小堆中，只有被水位没过的任务才弹出考察，不逐个重扫。
package alloc

import (
	"container/heap"

	"ontology/drf"
)

// Task 是一个待分配任务：单位需求向量 (CPU, Mem)。
type Task struct {
	ID       string
	CPU, Mem int64
}

// node 是堆元素：ratio 主导资源比例，floor 入场楼层。
type node struct {
	task         Task
	ratio, floor drf.Frac
	idx          int
}

type floorHeap []*node

func (h floorHeap) Len() int { return len(h) }
func (h floorHeap) Less(i, j int) bool {
	return drf.Cmp(h[i].floor, h[j].floor) < 0
}
func (h floorHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].idx, h[j].idx = i, j
}
func (h *floorHeap) Push(x any) { n := x.(*node); n.idx = len(*h); *h = append(*h, n) }
func (h *floorHeap) Pop() any   { old := *h; n := old[len(old)-1]; *h = old[:len(old)-1]; return n }

// Planner 持有容量与全部任务，状态只在进程内存。
type Planner struct {
	cpuCap, memCap int64
	tasks          []*node
	last           map[string]drf.Frac // 上次分配份额，作为本次楼层；nil=尚未分配
	examined       int                 // 非导出：上次 Allocate 中从堆弹出（参与拉平）的任务数
}

// New 创建容量为 (cpuCap, memCap) 的分配器（容量合法性由 api 层校验）。
func New(cpuCap, memCap int64) *Planner { return &Planner{cpuCap: cpuCap, memCap: memCap} }

// Add 追加一个任务（id/需求合法性由 api 层校验）；其楼层为 0。
func (p *Planner) Add(t Task) {
	p.tasks = append(p.tasks, &node{task: t, floor: drf.Frac{N: 0, D: 1}})
}

func zero() drf.Frac { return drf.Frac{N: 0, D: 1} }

// wCPU/wMem 是主导份额每提升 1 时该任务消耗的资源量 req/ratio。
func (n *node) wCPU() drf.Frac { return drf.Div(drf.Frac{N: n.task.CPU, D: 1}, n.ratio) }
func (n *node) wMem() drf.Frac { return drf.Div(drf.Frac{N: n.task.Mem, D: 1}, n.ratio) }

// Allocate 在既有分配之上单调推进（不回落），返回每任务单位数；无任务返回空映射。
func (p *Planner) Allocate() map[string]drf.Frac {
	if len(p.tasks) == 0 {
		p.last = map[string]drf.Frac{}
		return p.last
	}
	h := make(floorHeap, len(p.tasks))
	bC, bM := zero(), zero() // 全部任务停在楼层时的资源占用
	for i, nd := range p.tasks {
		nd.ratio = drf.DominantRatio(nd.task.CPU, nd.task.Mem, p.cpuCap, p.memCap)
		if f, ok := p.last[nd.task.ID]; ok {
			nd.floor = f
		}
		bC = drf.Add(bC, drf.Mul(nd.floor, nd.wCPU()))
		bM = drf.Add(bM, drf.Mul(nd.floor, nd.wMem()))
		h[i] = nd
	}
	heap.Init(&h)
	p.examined = 0
	level, uC, uM := zero(), bC, bM
	burnC, burnM := zero(), zero() // 活跃集合份额每抬升 1 的资源消耗
	for {
		// 水位没过楼层：floor==level 的任务入场（恰为堆顶的若干个）。
		for h.Len() > 0 && drf.Cmp(h[0].floor, level) == 0 {
			nd := heap.Pop(&h).(*node)
			p.examined++
			burnC = drf.Add(burnC, nd.wCPU())
			burnM = drf.Add(burnM, nd.wMem())
		}
		// 候选抬升量：到下一楼层的 gap 与两条约束允许的 Δr，取最小。
		// kind=1 楼层事件（汇入后继续），kind=2 绑定（终止）。
		var d drf.Frac
		kind := 0
		take := func(cand drf.Frac, k int) {
			if kind == 0 || drf.Cmp(cand, d) < 0 || (k == 2 && drf.Cmp(cand, d) == 0) {
				d, kind = cand, k
			}
		}
		if h.Len() > 0 {
			take(drf.Sub(h[0].floor, level), 1)
		}
		if drf.Cmp(burnC, zero()) > 0 {
			take(drf.Div(drf.Sub(drf.Frac{N: p.cpuCap, D: 1}, uC), burnC), 2)
		}
		if drf.Cmp(burnM, zero()) > 0 {
			take(drf.Div(drf.Sub(drf.Frac{N: p.memCap, D: 1}, uM), burnM), 2)
		}
		level = drf.Add(level, d)
		uC = drf.Add(uC, drf.Mul(d, burnC))
		uM = drf.Add(uM, drf.Mul(d, burnM))
		if kind == 2 {
			break
		}
	}
	out, shares := map[string]drf.Frac{}, map[string]drf.Frac{}
	for _, nd := range p.tasks { // 入场任务份额=水位，未入场停楼层；输出 a=份额/比例
		s := level
		if drf.Cmp(nd.floor, level) > 0 {
			s = nd.floor
		}
		shares[nd.task.ID] = s
		out[nd.task.ID] = drf.Div(s, nd.ratio)
	}
	p.last = shares
	return out
}
