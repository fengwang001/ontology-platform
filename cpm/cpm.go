// Package cpm 实现带搭接时距、约束与基线的增量关键路径（CPM）与时差维护器。
//
// 维护的数值定义：
//
//	ES(v) = max(snet(v), max_{(u,v,lag)} EF(u)+lag)，EF(v) = ES(v)+dur(v)
//	PF    = max_v EF(v)（无任务时为 0）
//	LF(v) = min(D, fnlt(v)（若有）, min_{(v,w,lag)} LS(w)-lag)，LS(v) = LF(v)-dur(v)
//	TF(v) = LF(v)-EF(v)（可为负）
//	FF(v) = min_{(v,w,lag)} (ES(w)-lag-EF(v))；无后继时 FF(v) = PF-EF(v)
//
// 关键任务为 TF 等于全部任务 TF 最小值的任务（有任务时必非空）。
// 依赖 (u,v,lag) 为驱动边当且仅当 ES(v) == EF(u)+lag。
//
// 所有更新与查询均可并发调用，内部以互斥锁串行化，结果等价于某个串行顺序，
// 查询不会看到只完成一半的更新。
package cpm

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrInvalidParam = errors.New("cpm: invalid parameter")
	ErrTaskNotFound = errors.New("cpm: task not found")
	ErrDepExists    = errors.New("cpm: dependency already exists")
	ErrTaskLimit    = errors.New("cpm: task count limit reached")
	ErrDepLimit     = errors.New("cpm: dependency count limit reached")
	ErrDepNotFound  = errors.New("cpm: dependency not found")
	ErrCycle        = errors.New("cpm: dependency would create a cycle")
	ErrNoBaseline   = errors.New("cpm: no baseline")
)

const (
	maxTasks    = 100000
	maxDeps     = 500000
	maxDeadline = int64(1000000000000)
	maxDuration = int64(1000000)
	maxLag      = int64(1000000)
	maxSNET     = int64(1000000000)
	maxFNLT     = int64(1000000000000)
	noFNLT      = int64(-1)
)

// Report 描述一次被接受的更新引起的变化。空列表以 nil 表示。
type Report struct {
	ChangedES   []int // ES 发生变化的任务编号（升序）
	ChangedLF   []int // LF 发生变化的任务编号（升序）
	OldPF       int64 // 更新前的项目完成时间
	NewPF       int64 // 更新后的项目完成时间
	CritAdded   []int // 进入关键任务集合的编号（升序）
	CritRemoved []int // 退出关键任务集合的编号（升序）
}

type edge struct {
	to  int
	lag int64
}

// CPM 是增量关键路径与时差维护器。零值不可用，须用 New 构造。
type CPM struct {
	mu sync.Mutex

	maxN     int
	maxE     int
	deadline int64

	n    int
	dur  []int64
	snet []int64
	fnlt []int64 // -1 表示无最晚完成约束
	es   []int64
	ef   []int64
	lf   []int64
	tf   []int64

	succ    [][]edge       // succ[u]：u 的后继边（to=v）
	pred    [][]edge       // pred[v]：v 的前驱边（to=u）
	succIdx map[[2]int]int // (u,v) -> 在 succ[u] 中的下标
	predIdx map[[2]int]int // (u,v) -> 在 pred[v] 中的下标
	ndeps   int
	rank    []int // 任务的一个拓扑序（对每条依赖 (u,v) 恒有 rank[u]<rank[v]）

	efCounts map[int64]int // EF 值 -> 任务数，用于维护 PF
	pf       int64

	tfBuckets map[int64]map[int]struct{} // TF 值 -> 任务集合，用于维护关键集合
	minTF     int64

	baseline []int64 // nil 表示尚未设置基线

	fwdEval int // 本次更新重算 ES 的次数（每次被接受的更新前清零）
	bwdEval int // 本次更新重算 LF 的次数（每次被接受的更新前清零）

	// 单次更新内的暂态（beginReport 初始化，finishReport 清空）。
	pending    map[int]int64 // 本次更新中 TF 发生变化的任务 -> 更新前 TF
	pendingNew map[int]bool  // 本次更新中新创建的任务
	oldMinTF   int64         // 更新前的最小 TF
}

// New 构造维护器。maxN 为任务数上限（1..100000），maxE 为依赖数上限
// （1..500000），deadline 为项目期限 D（0..1e12）。
func New(maxN, maxE int, deadline int64) (*CPM, error) {
	if maxN < 1 || maxN > maxTasks || maxE < 1 || maxE > maxDeps ||
		deadline < 0 || deadline > maxDeadline {
		return nil, ErrInvalidParam
	}
	return &CPM{
		maxN:      maxN,
		maxE:      maxE,
		deadline:  deadline,
		succIdx:   make(map[[2]int]int),
		predIdx:   make(map[[2]int]int),
		efCounts:  make(map[int64]int),
		tfBuckets: make(map[int64]map[int]struct{}),
	}, nil
}

// NumTasks 返回当前任务数。
func (c *CPM) NumTasks() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

// NumDeps 返回当前依赖数。
func (c *CPM) NumDeps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ndeps
}

func (c *CPM) beginReport() Report {
	c.fwdEval = 0
	c.bwdEval = 0
	c.pending = make(map[int]int64)
	c.pendingNew = make(map[int]bool)
	c.oldMinTF = c.minTF
	return Report{OldPF: c.pf}
}

func sortedInts(set map[int]bool) []int {
	if len(set) == 0 {
		return nil
	}
	out := make([]int, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

func (c *CPM) finishReport(rep *Report, changedES, changedLF map[int]bool) {
	if c.n == 0 {
		c.pf = 0
	} else if c.efCounts[c.pf] == 0 {
		max := int64(0)
		for k := range c.efCounts {
			if k > max {
				max = k
			}
		}
		c.pf = max
	}
	if c.n > 0 {
		if _, ok := c.tfBuckets[c.minTF]; !ok {
			first := true
			var m int64
			for k := range c.tfBuckets {
				if first || k < m {
					m = k
					first = false
				}
			}
			c.minTF = m
		}
	}
	rep.NewPF = c.pf
	rep.ChangedES = sortedInts(changedES)
	rep.ChangedLF = sortedInts(changedLF)

	// 关键集合差分：只检查 TF 变化过的任务与新任务；当最小 TF 发生移动时，
	// 未变 TF 的任务会整体进出，仅当桶中存在这类任务时才枚举（此时它们
	// 必然全部出现在输出中，枚举代价与输出规模同阶）。
	oldMin := c.oldMinTF
	newMin := c.minTF
	var added, removed []int
	if c.n > 0 {
		if newMin == oldMin {
			for v, old := range c.pending {
				cur := c.tf[v]
				if cur == newMin && old != oldMin {
					added = append(added, v)
				}
				if old == oldMin && cur != newMin {
					removed = append(removed, v)
				}
			}
			for v := range c.pendingNew {
				if c.tf[v] == newMin {
					added = append(added, v)
				}
			}
		} else {
			for v, old := range c.pending {
				cur := c.tf[v]
				was, is := old == oldMin, cur == newMin
				if is && !was {
					added = append(added, v)
				}
				if was && !is {
					removed = append(removed, v)
				}
			}
			for v := range c.pendingNew {
				if c.tf[v] == newMin {
					added = append(added, v)
				}
			}
			cntNew, cntOld := 0, 0
			for v := range c.pending {
				if c.tf[v] == newMin {
					cntNew++
				}
				if c.tf[v] == oldMin {
					cntOld++
				}
			}
			for v := range c.pendingNew {
				if c.tf[v] == newMin {
					cntNew++
				}
			}
			if b := c.tfBuckets[newMin]; len(b) > cntNew {
				for v := range b {
					if _, ok := c.pending[v]; ok {
						continue
					}
					if c.pendingNew[v] {
						continue
					}
					added = append(added, v)
				}
			}
			if b, ok := c.tfBuckets[oldMin]; ok && len(b) > cntOld {
				for v := range b {
					if _, ok := c.pending[v]; ok {
						continue
					}
					if c.pendingNew[v] {
						continue
					}
					removed = append(removed, v)
				}
			}
		}
	}
	sort.Ints(added)
	sort.Ints(removed)
	rep.CritAdded = added
	rep.CritRemoved = removed
	c.pending = nil
	c.pendingNew = nil
}

func (c *CPM) setEF(v int, val int64) {
	old := c.ef[v]
	if old == val {
		return
	}
	c.efCounts[old]--
	if c.efCounts[old] == 0 {
		delete(c.efCounts, old)
	}
	c.ef[v] = val
	c.efCounts[val]++
	if val > c.pf {
		c.pf = val
	}
	c.setTF(v)
}

func (c *CPM) setTF(v int) {
	nt := c.lf[v] - c.ef[v]
	if nt == c.tf[v] {
		return
	}
	old := c.tf[v]
	if c.pending != nil {
		if _, ok := c.pending[v]; !ok {
			c.pending[v] = old
		}
	}
	b := c.tfBuckets[old]
	delete(b, v)
	if len(b) == 0 {
		delete(c.tfBuckets, old)
	}
	c.tf[v] = nt
	nb := c.tfBuckets[nt]
	if nb == nil {
		nb = make(map[int]struct{})
		c.tfBuckets[nt] = nb
	}
	nb[v] = struct{}{}
	if nt < c.minTF {
		c.minTF = nt
	}
}

// evalES 重算任务 v 的 ES（计一次 fwdEval），返回 ES 是否变化。
func (c *CPM) evalES(v int, changed map[int]bool) bool {
	c.fwdEval++
	es := c.snet[v]
	for _, e := range c.pred[v] {
		if val := c.ef[e.to] + e.lag; val > es {
			es = val
		}
	}
	if es == c.es[v] {
		return false
	}
	c.es[v] = es
	c.setEF(v, es+c.dur[v])
	changed[v] = true
	return true
}

// evalLF 重算任务 v 的 LF（计一次 bwdEval），返回 LF 是否变化。
func (c *CPM) evalLF(v int, changed map[int]bool) bool {
	c.bwdEval++
	lf := c.deadline
	if c.fnlt[v] >= 0 && c.fnlt[v] < lf {
		lf = c.fnlt[v]
	}
	for _, e := range c.succ[v] {
		if val := c.lf[e.to] - c.dur[e.to] - e.lag; val < lf {
			lf = val
		}
	}
	if lf == c.lf[v] {
		return false
	}
	c.lf[v] = lf
	c.setTF(v)
	changed[v] = true
	return true
}

// rankItem 是传播堆的元素：按拓扑秩排序，保证处理顺序与拓扑序一致。
type rankItem struct {
	rank int
	node int
}

// rankHeap 是最小堆（forward 用，按秩升序处理，前驱先定值）。
type rankHeap []rankItem

func (h rankHeap) Len() int            { return len(h) }
func (h rankHeap) Less(i, j int) bool  { return h[i].rank < h[j].rank }
func (h rankHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *rankHeap) Push(x interface{}) { *h = append(*h, x.(rankItem)) }
func (h *rankHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// propagateForward 从 dirty0（需重算 ES 的任务）出发沿后继方向懒惰传播。
// 按拓扑秩升序处理：任务被处理时其所有前驱已取到终值，因此每个任务至多
// 被重算一次；变化停止时队列立即变空，不会触及未受影响的部分。
// 重算次数不超过 |dirty0| + Σ_{v∈ChangedES} 出度(v)，满足 fwdEval 上界。
func (c *CPM) propagateForward(dirty0 []int, changed map[int]bool) {
	if len(dirty0) == 0 {
		return
	}
	h := &rankHeap{}
	inHeap := make(map[int]bool, len(dirty0))
	push := func(v int) {
		if !inHeap[v] {
			inHeap[v] = true
			heap.Push(h, rankItem{rank: c.rank[v], node: v})
		}
	}
	for _, v := range dirty0 {
		push(v)
	}
	for h.Len() > 0 {
		w := heap.Pop(h).(rankItem).node
		delete(inHeap, w)
		if c.evalES(w, changed) {
			for _, e := range c.succ[w] {
				push(e.to)
			}
		}
	}
}

// propagateBackward 从 dirty0（需重算 LF 的任务）出发沿前驱方向懒惰传播。
// 按拓扑秩降序处理（后继先定值），机制与 propagateForward 对称，
// 重算次数不超过 |dirty0| + Σ_{v∈ChangedLF} 入度(v)，满足 bwdEval 上界。
func (c *CPM) propagateBackward(dirty0 []int, changed map[int]bool) {
	if len(dirty0) == 0 {
		return
	}
	h := &rankHeap{}
	inHeap := make(map[int]bool, len(dirty0))
	push := func(v int) {
		if !inHeap[v] {
			inHeap[v] = true
			heap.Push(h, rankItem{rank: -c.rank[v], node: v})
		}
	}
	for _, v := range dirty0 {
		push(v)
	}
	for h.Len() > 0 {
		w := heap.Pop(h).(rankItem).node
		delete(inHeap, w)
		if c.evalLF(w, changed) {
			for _, e := range c.pred[w] {
				push(e.to)
			}
		}
	}
}

// recomputeRanks 用 Kahn 算法重算全部任务的拓扑秩（确定性顺序）。
func (c *CPM) recomputeRanks() {
	indeg := make([]int, c.n)
	for v := 0; v < c.n; v++ {
		indeg[v] = len(c.pred[v])
	}
	var queue []int
	for v := 0; v < c.n; v++ {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	r := 0
	for len(queue) > 0 {
		u := queue[0]
		queue = queue[1:]
		c.rank[u] = r
		r++
		for _, e := range c.succ[u] {
			indeg[e.to]--
			if indeg[e.to] == 0 {
				queue = append(queue, e.to)
			}
		}
	}
}

func (c *CPM) succIDs(v int) []int {
	out := make([]int, 0, len(c.succ[v]))
	for _, e := range c.succ[v] {
		out = append(out, e.to)
	}
	return out
}

func (c *CPM) predIDs(v int) []int {
	out := make([]int, 0, len(c.pred[v]))
	for _, e := range c.pred[v] {
		out = append(out, e.to)
	}
	return out
}

// AddTask 新增任务，返回从 0 起严格递增的编号与变更报告。
// 新任务 snet=0、无 fnlt 约束。dur 须在 [0, 1e6] 内。
func (c *CPM) AddTask(dur int64) (int, Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dur < 0 || dur > maxDuration {
		return -1, Report{}, ErrInvalidParam
	}
	if c.n >= c.maxN {
		return -1, Report{}, ErrTaskLimit
	}
	rep := c.beginReport()
	id := c.n
	c.dur = append(c.dur, dur)
	c.snet = append(c.snet, 0)
	c.fnlt = append(c.fnlt, noFNLT)
	c.es = append(c.es, 0)
	c.ef = append(c.ef, dur)
	c.lf = append(c.lf, c.deadline)
	c.tf = append(c.tf, c.deadline-dur)
	c.succ = append(c.succ, nil)
	c.pred = append(c.pred, nil)
	c.rank = append(c.rank, id) // 新任务秩大于所有现有秩，保持拓扑性质
	c.n++
	c.efCounts[dur]++
	if dur > c.pf {
		c.pf = dur
	}
	tf := c.deadline - dur
	b := c.tfBuckets[tf]
	if b == nil {
		b = make(map[int]struct{})
		c.tfBuckets[tf] = b
	}
	b[id] = struct{}{}
	if id == 0 || tf < c.minTF {
		c.minTF = tf
	}
	c.pendingNew[id] = true
	c.finishReport(&rep, nil, nil)
	return id, rep, nil
}

// createsCycle 报告加入依赖 (u,v) 是否会成环（含 u==v）。
func (c *CPM) createsCycle(u, v int) bool {
	if u == v {
		return true
	}
	seen := map[int]bool{v: true}
	stack := []int{v}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if x == u {
			return true
		}
		for _, e := range c.succ[x] {
			if !seen[e.to] {
				seen[e.to] = true
				stack = append(stack, e.to)
			}
		}
	}
	return false
}

// AddDep 添加依赖 (u,v,lag)：v 的 ES 不早于 u 的 EF 加 lag（负值为提前搭接）。
// 拒绝原因按参数非法、任务不存在、依赖已存在、依赖数已满、成环的顺序只报第一个。
func (c *CPM) AddDep(u, v int, lag int64) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if lag < -maxLag || lag > maxLag {
		return Report{}, ErrInvalidParam
	}
	if u < 0 || u >= c.n || v < 0 || v >= c.n {
		return Report{}, ErrTaskNotFound
	}
	key := [2]int{u, v}
	if _, ok := c.succIdx[key]; ok {
		return Report{}, ErrDepExists
	}
	if c.ndeps >= c.maxE {
		return Report{}, ErrDepLimit
	}
	// 若新边与现有拓扑序一致（rank[u]<rank[v]），该序仍是新图的拓扑序，
	// 故必不成环，可跳过可达性检查；否则先检查，接受后重算拓扑秩。
	needReRank := false
	if c.rank[u] >= c.rank[v] {
		if c.createsCycle(u, v) {
			return Report{}, ErrCycle
		}
		needReRank = true
	}
	rep := c.beginReport()
	c.succIdx[key] = len(c.succ[u])
	c.succ[u] = append(c.succ[u], edge{to: v, lag: lag})
	c.predIdx[key] = len(c.pred[v])
	c.pred[v] = append(c.pred[v], edge{to: u, lag: lag})
	c.ndeps++
	if needReRank {
		c.recomputeRanks()
	}
	changedES := make(map[int]bool)
	c.propagateForward([]int{v}, changedES)
	changedLF := make(map[int]bool)
	c.propagateBackward([]int{u}, changedLF)
	c.finishReport(&rep, changedES, changedLF)
	return rep, nil
}

// RemoveDep 删除依赖 (u,v)。拒绝原因按任务不存在、依赖不存在的顺序只报第一个。
func (c *CPM) RemoveDep(u, v int) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u < 0 || u >= c.n || v < 0 || v >= c.n {
		return Report{}, ErrTaskNotFound
	}
	key := [2]int{u, v}
	si, ok := c.succIdx[key]
	if !ok {
		return Report{}, ErrDepNotFound
	}
	rep := c.beginReport()
	last := len(c.succ[u]) - 1
	if si != last {
		c.succ[u][si] = c.succ[u][last]
		c.succIdx[[2]int{u, c.succ[u][si].to}] = si
	}
	c.succ[u] = c.succ[u][:last]
	delete(c.succIdx, key)
	pi := c.predIdx[key]
	last = len(c.pred[v]) - 1
	if pi != last {
		c.pred[v][pi] = c.pred[v][last]
		c.predIdx[[2]int{c.pred[v][pi].to, v}] = pi
	}
	c.pred[v] = c.pred[v][:last]
	delete(c.predIdx, key)
	c.ndeps--
	changedES := make(map[int]bool)
	c.propagateForward([]int{v}, changedES)
	changedLF := make(map[int]bool)
	c.propagateBackward([]int{u}, changedLF)
	c.finishReport(&rep, changedES, changedLF)
	return rep, nil
}

// SetDuration 修改任务 v 的工期（0..1e6）。
func (c *CPM) SetDuration(v int, dur int64) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if dur < 0 || dur > maxDuration {
		return Report{}, ErrInvalidParam
	}
	if v < 0 || v >= c.n {
		return Report{}, ErrTaskNotFound
	}
	rep := c.beginReport()
	changedES := make(map[int]bool)
	changedLF := make(map[int]bool)
	if dur != c.dur[v] {
		c.dur[v] = dur
		c.setEF(v, c.es[v]+dur)
		c.propagateForward(c.succIDs(v), changedES)
		c.propagateBackward(c.predIDs(v), changedLF)
	}
	c.finishReport(&rep, changedES, changedLF)
	return rep, nil
}

// SetConstraint 设置任务 v 的最早开始约束 snet（0..1e9）与最晚完成约束
// fnlt（0..1e12，-1 表示无）。
func (c *CPM) SetConstraint(v int, snet, fnlt int64) (Report, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if snet < 0 || snet > maxSNET || fnlt < noFNLT || fnlt > maxFNLT {
		return Report{}, ErrInvalidParam
	}
	if v < 0 || v >= c.n {
		return Report{}, ErrTaskNotFound
	}
	rep := c.beginReport()
	changedES := make(map[int]bool)
	changedLF := make(map[int]bool)
	if snet != c.snet[v] || fnlt != c.fnlt[v] {
		c.snet[v] = snet
		c.fnlt[v] = fnlt
		c.propagateForward([]int{v}, changedES)
		c.propagateBackward([]int{v}, changedLF)
	}
	c.finishReport(&rep, changedES, changedLF)
	return rep, nil
}

// SetBaseline 记录当前全部任务的 EF 作为基线（覆盖此前的基线）。
func (c *CPM) SetBaseline() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.baseline = append(c.baseline[:0], c.ef...)
}

// Variance 返回 EF(v) 减去基线 EF(v)。基线之后新建的任务或尚无基线时
// 返回 ErrNoBaseline；任务不存在时返回 ErrTaskNotFound。
func (c *CPM) Variance(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if v < 0 || v >= c.n {
		return 0, ErrTaskNotFound
	}
	if c.baseline == nil || v >= len(c.baseline) {
		return 0, ErrNoBaseline
	}
	return c.ef[v] - c.baseline[v], nil
}

func (c *CPM) validTask(v int) bool { return v >= 0 && v < c.n }

// ES 返回任务 v 的最早开始时间。
func (c *CPM) ES(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	return c.es[v], nil
}

// EF 返回任务 v 的最早完成时间。
func (c *CPM) EF(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	return c.ef[v], nil
}

// LS 返回任务 v 的最晚开始时间。
func (c *CPM) LS(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	return c.lf[v] - c.dur[v], nil
}

// LF 返回任务 v 的最晚完成时间。
func (c *CPM) LF(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	return c.lf[v], nil
}

// TF 返回任务 v 的总时差（可为负）。
func (c *CPM) TF(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	return c.tf[v], nil
}

// FF 返回任务 v 的自由时差：所有后继 ES(w)-lag-EF(v) 的最小值；
// 无后继时取 PF-EF(v)。
func (c *CPM) FF(v int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.validTask(v) {
		return 0, ErrTaskNotFound
	}
	if len(c.succ[v]) == 0 {
		return c.pf - c.ef[v], nil
	}
	ff := c.es[c.succ[v][0].to] - c.succ[v][0].lag - c.ef[v]
	for _, e := range c.succ[v][1:] {
		if val := c.es[e.to] - e.lag - c.ef[v]; val < ff {
			ff = val
		}
	}
	return ff, nil
}

// PF 返回项目完成时间（全部任务 EF 的最大值，无任务时为 0）。
func (c *CPM) PF() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pf
}

// sortedCrit 返回当前关键任务编号（升序）；无任务时为 nil。
func (c *CPM) sortedCrit() []int {
	b := c.tfBuckets[c.minTF]
	if len(b) == 0 {
		return nil
	}
	out := make([]int, 0, len(b))
	for v := range b {
		out = append(out, v)
	}
	sort.Ints(out)
	return out
}

// CriticalTasks 返回当前关键任务编号（升序）。有任务时必非空。
func (c *CPM) CriticalTasks() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sortedCrit()
}

// CriticalPath 返回关键路径：在关键任务中取没有来自关键任务的驱动边的
// 编号最小者作起点，之后每步取当前任务指向关键任务的驱动边终点中编号最小者，
// 直到没有为止；无任务时为空。
func (c *CPM) CriticalPath() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.n == 0 {
		return nil
	}
	crit := c.tfBuckets[c.minTF]
	start := -1
	for _, v := range c.sortedCrit() {
		hasCritDriver := false
		for _, e := range c.pred[v] {
			if _, ok := crit[e.to]; ok && c.es[v] == c.ef[e.to]+e.lag {
				hasCritDriver = true
				break
			}
		}
		if !hasCritDriver {
			start = v
			break
		}
	}
	path := []int{start}
	cur := start
	for {
		next := -1
		for _, e := range c.succ[cur] {
			w := e.to
			if _, ok := crit[w]; ok && c.es[w] == c.ef[cur]+e.lag {
				if next == -1 || w < next {
					next = w
				}
			}
		}
		if next == -1 {
			break
		}
		path = append(path, next)
		cur = next
	}
	return path
}
