// Package planner 实现流水线区域故障恢复规划器。
//
// 作业图由任务与有向边组成，边分为流水线边与阻塞边。
// 仅由流水线边（不计方向）连通的任务构成一个区域，区域编号取区域内最小任务编号。
// 失败上报时按三条规则迭代算出必须重启的最小区域集合。
package planner

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// EdgeType 表示边的类型。
type EdgeType int

const (
	// Pipeline 为流水线边，仅用于区域划分。
	Pipeline EdgeType = iota
	// Blocking 为阻塞边，产生可被下游消费的阻塞结果。
	Blocking
)

func (t EdgeType) String() string {
	if t == Blocking {
		return "blocking"
	}
	return "pipeline"
}

// Edge 为一条有向边，From 为生产者，To 为消费者。
type Edge struct {
	From int
	To   int
	Type EdgeType
}

// TaskState 表示任务运行状态。
type TaskState int

const (
	// Running 表示任务正在运行（初始状态）。
	Running TaskState = iota
	// Completed 表示任务已完成。
	Completed
)

func (s TaskState) String() string {
	if s == Completed {
		return "completed"
	}
	return "running"
}

// ErrKind 区分各类可识别的失败原因。
type ErrKind int

const (
	// ErrTaskNotFound 边或上报引用了不存在的任务。
	ErrTaskNotFound ErrKind = iota
	// ErrDuplicateEdge 建图时出现同一有序任务对的重复边。
	ErrDuplicateEdge
	// ErrBlockingSameRegion 阻塞边两端落在同一区域内。
	ErrBlockingSameRegion
	// ErrRegionCycle 区域之间（经阻塞边）成环。
	ErrRegionCycle
	// ErrTaskNotRunning 上报针对的任务不在运行中。
	ErrTaskNotRunning
	// ErrEdgeNotFound 上报引用了不存在的边。
	ErrEdgeNotFound
	// ErrNotBlockingEdge 结果丢失上报针对的边不是阻塞边。
	ErrNotBlockingEdge
	// ErrResultNotProduced 结果尚未产出（生产者未完成）。
	ErrResultNotProduced
)

// Error 为可区分原因的操作错误。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// IsKind 判断 err 是否为指定原因。
func IsKind(err error, kind ErrKind) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Kind == kind
}

func newError(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// edgeInfo 记录一条边的丢失标记。
type edgeInfo struct {
	edge Edge
	lost bool
}

// Planner 为故障恢复规划器，所有上报与查询均可并发调用，
// 效果等价于某个串行顺序（内部以互斥锁串行化）。
type Planner struct {
	mu sync.Mutex

	taskIDs   []int // 升序任务编号
	states    map[int]TaskState
	edges     map[[2]int]*edgeInfo
	edgeOrder []Edge // 建图顺序，用于确定性遍历

	regionOf  map[int]int   // 任务 -> 区域编号
	regions   map[int][]int // 区域编号 -> 升序任务列表
	regionIDs []int         // 升序区域编号

	// 区域级阻塞边邻接（均为跨区域边）。
	consumersOf map[int][]int // 生产区域 -> 消费区域（升序、去重）
	producersOf map[int][]int // 消费区域 -> 生产区域（升序、去重）
	// 消费区域 -> 进入该区域的阻塞边（用于可用性判定）。
	inEdges map[int][]Edge
}

// Build 校验并构建规划器。任何校验失败都整体拒绝，不保留任何状态。
// 多因并存时按：任务不存在 -> 重复边 -> 阻塞边同区域 -> 区域成环 的顺序只报第一个。
func Build(taskIDs []int, edges []Edge) (*Planner, error) {
	p := &Planner{
		states:      make(map[int]TaskState, len(taskIDs)),
		edges:       make(map[[2]int]*edgeInfo, len(edges)),
		regionOf:    make(map[int]int, len(taskIDs)),
		regions:     make(map[int][]int),
		consumersOf: make(map[int][]int),
		producersOf: make(map[int][]int),
		inEdges:     make(map[int][]Edge),
	}
	p.taskIDs = append(p.taskIDs, taskIDs...)
	sort.Ints(p.taskIDs)
	for _, id := range p.taskIDs {
		p.states[id] = Running
	}
	p.edgeOrder = append(p.edgeOrder, edges...)

	if err := p.validateAndIndex(); err != nil {
		return nil, err
	}
	return p, nil
}

// validateAndIndex 依次执行全部建图校验并建立索引。
func (p *Planner) validateAndIndex() error {
	if err := p.checkEdgeTasks(); err != nil {
		return err
	}
	if err := p.checkDuplicateEdges(); err != nil {
		return err
	}
	p.computeRegions()
	if err := p.checkBlockingSameRegion(); err != nil {
		return err
	}
	if err := p.checkRegionCycle(); err != nil {
		return err
	}
	return nil
}

// checkEdgeTasks 校验所有边引用的任务都存在。
func (p *Planner) checkEdgeTasks() error {
	for _, e := range p.edgeOrder {
		if _, ok := p.states[e.From]; !ok {
			return newError(ErrTaskNotFound, "边 %d->%d 引用了不存在的任务 %d", e.From, e.To, e.From)
		}
		if _, ok := p.states[e.To]; !ok {
			return newError(ErrTaskNotFound, "边 %d->%d 引用了不存在的任务 %d", e.From, e.To, e.To)
		}
	}
	return nil
}

// checkDuplicateEdges 校验不存在同一有序任务对的重复边，并建立边索引。
func (p *Planner) checkDuplicateEdges() error {
	for _, e := range p.edgeOrder {
		key := [2]int{e.From, e.To}
		if _, dup := p.edges[key]; dup {
			return newError(ErrDuplicateEdge, "重复边 %d->%d", e.From, e.To)
		}
		p.edges[key] = &edgeInfo{edge: e}
	}
	return nil
}

// computeRegions 用流水线边（不计方向）做连通分量划分，
// 区域编号取区域内最小任务编号，并建立区域级阻塞边邻接索引。
func (p *Planner) computeRegions() {
	adj := make(map[int][]int, len(p.taskIDs))
	for _, e := range p.edgeOrder {
		if e.Type != Pipeline {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
		adj[e.To] = append(adj[e.To], e.From)
	}

	visited := make(map[int]bool, len(p.taskIDs))
	for _, start := range p.taskIDs {
		if visited[start] {
			continue
		}
		var members []int
		stack := []int{start}
		visited[start] = true
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			members = append(members, cur)
			for _, next := range adj[cur] {
				if !visited[next] {
					visited[next] = true
					stack = append(stack, next)
				}
			}
		}
		sort.Ints(members)
		regionID := members[0]
		p.regions[regionID] = members
		for _, id := range members {
			p.regionOf[id] = regionID
		}
	}
	for id := range p.regions {
		p.regionIDs = append(p.regionIDs, id)
	}
	sort.Ints(p.regionIDs)

	consumerSet := make(map[int]map[int]bool)
	producerSet := make(map[int]map[int]bool)
	for _, e := range p.edgeOrder {
		if e.Type != Blocking {
			continue
		}
		from, to := p.regionOf[e.From], p.regionOf[e.To]
		if consumerSet[from] == nil {
			consumerSet[from] = make(map[int]bool)
		}
		if producerSet[to] == nil {
			producerSet[to] = make(map[int]bool)
		}
		if !consumerSet[from][to] {
			consumerSet[from][to] = true
			p.consumersOf[from] = append(p.consumersOf[from], to)
		}
		if !producerSet[to][from] {
			producerSet[to][from] = true
			p.producersOf[to] = append(p.producersOf[to], from)
		}
		p.inEdges[to] = append(p.inEdges[to], e)
	}
	for _, list := range p.consumersOf {
		sort.Ints(list)
	}
	for _, list := range p.producersOf {
		sort.Ints(list)
	}
}

// checkBlockingSameRegion 校验阻塞边两端不在同一区域。
func (p *Planner) checkBlockingSameRegion() error {
	for _, e := range p.edgeOrder {
		if e.Type != Blocking {
			continue
		}
		if p.regionOf[e.From] == p.regionOf[e.To] {
			return newError(ErrBlockingSameRegion,
				"阻塞边 %d->%d 两端同属区域 %d", e.From, e.To, p.regionOf[e.From])
		}
	}
	return nil
}

// checkRegionCycle 校验区域间（经阻塞边）不成环。
func (p *Planner) checkRegionCycle() error {
	const (
		white = 0 // 未访问
		gray  = 1 // 在栈上
		black = 2 // 已完成
	)
	color := make(map[int]int, len(p.regionIDs))
	var visit func(r int) bool
	visit = func(r int) bool {
		color[r] = gray
		for _, next := range p.consumersOf[r] {
			switch color[next] {
			case gray:
				return true
			case white:
				if visit(next) {
					return true
				}
			}
		}
		color[r] = black
		return false
	}
	for _, r := range p.regionIDs {
		if color[r] == white && visit(r) {
			return newError(ErrRegionCycle, "区域 %d 之间存在环", r)
		}
	}
	return nil
}

// ReportComplete 完成上报：仅对运行中任务有效，使其变为已完成。
func (p *Planner) ReportComplete(taskID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reportCompleteLocked(taskID)
}

func (p *Planner) reportCompleteLocked(taskID int) error {
	state, ok := p.states[taskID]
	if !ok {
		return newError(ErrTaskNotFound, "任务 %d 不存在", taskID)
	}
	if state != Running {
		return newError(ErrTaskNotRunning, "任务 %d 未在运行（当前状态 %s）", taskID, state)
	}
	p.states[taskID] = Completed
	return nil
}

// ReportFail 失败上报：计算必须重启的最小区域集合，
// 集合内全部任务变为运行中，其所产阻塞结果的丢失标记清除。
// 返回升序的区域编号与任务编号。
func (p *Planner) ReportFail(taskID int) (regions []int, tasks []int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	state, ok := p.states[taskID]
	if !ok {
		return nil, nil, newError(ErrTaskNotFound, "任务 %d 不存在", taskID)
	}
	if state != Running {
		return nil, nil, newError(ErrTaskNotRunning, "任务 %d 未在运行（当前状态 %s）", taskID, state)
	}

	set := p.restartSetLocked(p.regionOf[taskID])

	regions = append(regions, set...)
	inSet := make(map[int]bool, len(set))
	for _, r := range set {
		inSet[r] = true
	}
	for _, r := range set {
		tasks = append(tasks, p.regions[r]...)
		for _, id := range p.regions[r] {
			p.states[id] = Running
		}
	}
	sort.Ints(tasks)

	// 清除集合内区域所产阻塞结果的丢失标记。
	for _, info := range p.edges {
		if info.edge.Type == Blocking && info.lost && inSet[p.regionOf[info.edge.From]] {
			info.lost = false
		}
	}
	return regions, tasks, nil
}

// restartSetLocked 计算含 startRegion 的最小重启区域集合。
// 三条规则迭代至不动点：
//  1. 集合含失败任务所在区域（由调用方保证 startRegion 入集合）；
//  2. 集合内区域所产阻塞结果的全部消费区域也在集合内；
//  3. 集合内区域消费的某阻塞结果不可用且其生产者已完成时，
//     生产者所在区域也在集合内（生产者仍在运行则不加入）。
func (p *Planner) restartSetLocked(startRegion int) []int {
	inSet := map[int]bool{startRegion: true}
	for changed := true; changed; {
		changed = false
		for _, r := range p.regionIDs {
			if !inSet[r] {
				continue
			}
			// 规则 2：消费本区域阻塞结果的区域连带重启。
			for _, consumer := range p.consumersOf[r] {
				if !inSet[consumer] {
					inSet[consumer] = true
					changed = true
				}
			}
			// 规则 3：消费了不可用阻塞结果且生产者已完成，拉入生产者区域。
			for _, e := range p.inEdges[r] {
				if p.states[e.From] != Completed {
					continue // 生产者仍在运行，不加入
				}
				if !p.edges[[2]int{e.From, e.To}].lost {
					continue // 结果可用，无需拉入
				}
				producerRegion := p.regionOf[e.From]
				if !inSet[producerRegion] {
					inSet[producerRegion] = true
					changed = true
				}
			}
		}
	}
	var set []int
	for _, r := range p.regionIDs {
		if inSet[r] {
			set = append(set, r)
		}
	}
	return set
}

// ReportLoss 结果丢失上报：要求边存在、为阻塞边且生产者已完成。
// 已丢失的再报视为成功且无变化。
func (p *Planner) ReportLoss(from, to int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := p.states[from]; !ok {
		return newError(ErrTaskNotFound, "任务 %d 不存在", from)
	}
	if _, ok := p.states[to]; !ok {
		return newError(ErrTaskNotFound, "任务 %d 不存在", to)
	}
	info, ok := p.edges[[2]int{from, to}]
	if !ok {
		return newError(ErrEdgeNotFound, "边 %d->%d 不存在", from, to)
	}
	if info.edge.Type != Blocking {
		return newError(ErrNotBlockingEdge, "边 %d->%d 不是阻塞边", from, to)
	}
	if p.states[from] != Completed {
		return newError(ErrResultNotProduced, "边 %d->%d 的结果尚未产出（生产者未完成）", from, to)
	}
	if info.lost {
		return nil // 已丢失，再报视为成功且无变化
	}
	info.lost = true
	return nil
}

// Snapshot 为一致性查询：返回全部任务状态与已丢失的阻塞边。
func (p *Planner) Snapshot() (states map[int]TaskState, lostEdges []Edge) {
	p.mu.Lock()
	defer p.mu.Unlock()

	states = make(map[int]TaskState, len(p.states))
	for id, s := range p.states {
		states[id] = s
	}
	for _, e := range p.edgeOrder {
		if info := p.edges[[2]int{e.From, e.To}]; info.edge.Type == Blocking && info.lost {
			lostEdges = append(lostEdges, info.edge)
		}
	}
	return states, lostEdges
}

// RegionOf 查询任务所属区域编号。
func (p *Planner) RegionOf(taskID int) (int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.regionOf[taskID]
	return r, ok
}
