package pipeline

import (
	"io"
	"log/slog"
	"os"
	"sort"
	"sync"
)

// EdgeKind 标记边的类型。
type EdgeKind int

const (
	// Pipeline 边仅用于区域连通性。
	Pipeline EdgeKind = iota
	// Blocking 边携带跨区域的阻塞结果。
	Blocking
)

// Edge 描述一条有向边：From 生产，To 消费。
type Edge struct {
	From int
	To   int
	Kind EdgeKind
}

// RestartPlan 是一次失败上报的重启规划结果。
type RestartPlan struct {
	Regions []int
	Tasks   []int
}

// Planner 是流水线区域故障恢复规划器。
type Planner struct {
	mu sync.Mutex

	log *slog.Logger

	tasks      map[int]bool
	taskRegion map[int]int
	regions    []int
	regionTask map[int][]int

	// allEdges 记录全部边（有序对 -> 类型），blockFrom 为阻塞边的稳定遍历顺序。
	allEdges  map[[2]int]EdgeKind
	blockFrom [][2]int

	// completed 为已完成任务；lost 为已上报丢失的阻塞边。
	completed map[int]bool
	lost      map[[2]int]bool
}

// Option 配置 Planner。
type Option func(*Planner)

// WithLogger 指定判定日志输出位置；nil 表示丢弃日志。
func WithLogger(w io.Writer) Option {
	return func(p *Planner) {
		if w == nil {
			p.log = slog.New(slog.NewTextHandler(io.Discard, nil))
			return
		}
		p.log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
}

// New 创建规划器并校验、构建作业图。
//
// 校验顺序（多因并存只报第一个）：
//  1. 边引用不存在的任务；
//  2. 重复边（同一有序任务对）；
//  3. 阻塞边两端落在同一区域；
//  4. 区域间依赖图成环。
func New(taskIDs []int, edges []Edge, opts ...Option) (*Planner, error) {
	p := &Planner{
		log:        slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		tasks:      make(map[int]bool),
		taskRegion: make(map[int]int),
		regionTask: make(map[int][]int),
		allEdges:   make(map[[2]int]EdgeKind),
		completed:  make(map[int]bool),
		lost:       make(map[[2]int]bool),
	}
	for _, opt := range opts {
		opt(p)
	}

	p.log.Info("build graph: input", "tasks", taskIDs, "edges", edgeLogList(edges))

	for _, id := range taskIDs {
		p.tasks[id] = true
	}

	// 1 & 2：按边的给出顺序逐条校验未知端点与重复有序对。
	seen := make(map[[2]int]EdgeKind)
	for _, e := range edges {
		if !p.tasks[e.From] || !p.tasks[e.To] {
			p.log.Info("build graph: rejected", "reason", ErrUnknownTaskEndpoint, "edge", e)
			return nil, ErrUnknownTaskEndpoint
		}
		key := [2]int{e.From, e.To}
		if _, dup := seen[key]; dup {
			p.log.Info("build graph: rejected", "reason", ErrDuplicateEdge, "edge", e)
			return nil, ErrDuplicateEdge
		}
		seen[key] = e.Kind
	}

	// 区域划分：忽略方向，仅由流水线边连通的任务构成同一区域。
	parent := make(map[int]int)
	var find func(int) int
	find = func(x int) int {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for id := range p.tasks {
		parent[id] = id
	}
	for _, e := range edges {
		if e.Kind == Pipeline {
			ra, rb := find(e.From), find(e.To)
			if ra != rb {
				parent[rb] = ra
			}
		}
	}

	// 区域编号取区域内最小任务编号。
	minTask := make(map[int]int)
	for id := range p.tasks {
		root := find(id)
		if m, ok := minTask[root]; !ok || id < m {
			minTask[root] = id
		}
	}
	regionMembers := make(map[int][]int)
	for id := range p.tasks {
		rid := minTask[find(id)]
		p.taskRegion[id] = rid
		regionMembers[rid] = append(regionMembers[rid], id)
	}
	for rid, members := range regionMembers {
		sort.Ints(members)
		p.regionTask[rid] = members
		p.regions = append(p.regions, rid)
	}
	sort.Ints(p.regions)
	p.log.Info("build graph: region partition", "regions", p.regions, "members", p.regionTask)

	// 3：阻塞边两端不得在同一区域。
	for _, e := range edges {
		key := [2]int{e.From, e.To}
		p.allEdges[key] = e.Kind
		if e.Kind != Blocking {
			continue
		}
		p.blockFrom = append(p.blockFrom, key)
		if p.taskRegion[e.From] == p.taskRegion[e.To] {
			p.log.Info("build graph: rejected", "reason", ErrIntraRegionBlock, "edge", e)
			return nil, ErrIntraRegionBlock
		}
	}

	// 4：区域之间（以阻塞边为有向依赖）不得成环。
	if p.regionGraphHasCycle() {
		p.log.Info("build graph: rejected", "reason", ErrRegionCycle)
		return nil, ErrRegionCycle
	}
	p.log.Info("build graph: accepted", "blocking_edges", p.blockFrom)

	return p, nil
}

// Complete 上报任务完成。仅对运行中任务有效。
func (p *Planner) Complete(taskID int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.log.Info("complete: input", "task", taskID)
	if !p.tasks[taskID] {
		p.log.Info("complete: rejected", "task", taskID, "reason", ErrTaskNotFound)
		return ErrTaskNotFound
	}
	if p.completed[taskID] {
		p.log.Info("complete: rejected", "task", taskID, "reason", ErrTaskNotRunning)
		return ErrTaskNotRunning
	}
	p.completed[taskID] = true
	p.log.Info("complete: accepted", "task", taskID, "region", p.taskRegion[taskID])
	return nil
}

// Fail 上报任务失败，返回最小重启集合并应用重启。
func (p *Planner) Fail(taskID int) (RestartPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.log.Info("fail: input", "task", taskID)
	if !p.tasks[taskID] {
		p.log.Info("fail: rejected", "task", taskID, "reason", ErrTaskNotFound)
		return RestartPlan{}, ErrTaskNotFound
	}
	if p.completed[taskID] {
		p.log.Info("fail: rejected", "task", taskID, "reason", ErrTaskNotRunning)
		return RestartPlan{}, ErrTaskNotRunning
	}

	plan := p.computeRestartLocked(taskID)

	// 应用重启：集合内全部任务回到运行中，其产出的阻塞结果丢失标记清除。
	inSet := make(map[int]bool, len(plan.Regions))
	for _, r := range plan.Regions {
		inSet[r] = true
		for _, t := range p.regionTask[r] {
			delete(p.completed, t)
		}
	}
	for key, kind := range p.allEdges {
		if kind == Blocking && inSet[p.taskRegion[key[0]]] {
			delete(p.lost, key)
		}
	}
	p.log.Info("fail: state reset applied", "regions", plan.Regions, "tasks", plan.Tasks)
	return plan, nil
}

// LoseResult 上报阻塞边结果丢失。已丢失再报视为成功且无变化。
func (p *Planner) LoseResult(from, to int) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := [2]int{from, to}
	p.log.Info("lose_result: input", "edge", key)
	if !p.tasks[from] || !p.tasks[to] {
		p.log.Info("lose_result: rejected", "edge", key, "reason", ErrTaskNotFound)
		return ErrTaskNotFound
	}
	kind, exists := p.allEdges[key]
	if !exists {
		p.log.Info("lose_result: rejected", "edge", key, "reason", ErrEdgeNotFound)
		return ErrEdgeNotFound
	}
	if kind != Blocking {
		p.log.Info("lose_result: rejected", "edge", key, "reason", ErrNonBlockingEdge)
		return ErrNonBlockingEdge
	}
	if !p.completed[from] {
		p.log.Info("lose_result: rejected", "edge", key,
			"reason", ErrResultNotProduced,
			"basis", "producer task still running, result never produced")
		return ErrResultNotProduced
	}
	if p.lost[key] {
		p.log.Info("lose_result: accepted without change",
			"edge", key, "basis", "result already marked lost")
		return nil
	}
	p.lost[key] = true
	p.log.Info("lose_result: accepted", "edge", key,
		"basis", "producer completed and result available, now marked lost")
	return nil
}

// RestartPlanFor 只读地给出某任务失败时的重启集合，不改变任何状态。
func (p *Planner) RestartPlanFor(taskID int) (RestartPlan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tasks[taskID] {
		return RestartPlan{}, ErrTaskNotFound
	}
	return p.computeRestartLocked(taskID), nil
}

// IsCompleted 查询任务是否已完成。
func (p *Planner) IsCompleted(taskID int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.tasks[taskID] {
		return false, ErrTaskNotFound
	}
	return p.completed[taskID], nil
}

// RegionOf 查询任务所属区域编号。
func (p *Planner) RegionOf(taskID int) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.taskRegion[taskID]
	if !ok {
		return 0, ErrTaskNotFound
	}
	return r, nil
}

// ResultAvailable 查询阻塞结果当前是否可用。
func (p *Planner) ResultAvailable(from, to int) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := [2]int{from, to}
	if !p.tasks[from] || !p.tasks[to] {
		return false, ErrTaskNotFound
	}
	kind, exists := p.allEdges[key]
	if !exists {
		return false, ErrEdgeNotFound
	}
	if kind != Blocking {
		return false, ErrNonBlockingEdge
	}
	return p.completed[from] && !p.lost[key], nil
}

// Regions 返回全部区域编号（升序）。
func (p *Planner) Regions() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.regions))
	copy(out, p.regions)
	return out
}

// RegionTasks 返回区域内全部任务编号（升序）。
func (p *Planner) RegionTasks(region int) ([]int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	members, ok := p.regionTask[region]
	if !ok {
		return nil, ErrTaskNotFound
	}
	out := make([]int, len(members))
	copy(out, members)
	return out, nil
}

// computeRestartLocked 按三条规则迭代求最小重启区域闭包。调用方持锁。
func (p *Planner) computeRestartLocked(failedTask int) RestartPlan {
	startRegion := p.taskRegion[failedTask]
	inSet := map[int]bool{startRegion: true}
	p.log.Info("restart closure: seed", "task", failedTask, "region", startRegion)

	for round := 1; ; round++ {
		addedCount := 0
		for _, key := range p.blockFrom {
			producer, consumer := key[0], key[1]
			pr, cr := p.taskRegion[producer], p.taskRegion[consumer]

			// 规则二：集合内区域产出的阻塞结果，其消费区域必须同入集合。
			if inSet[pr] && !inSet[cr] {
				inSet[cr] = true
				addedCount++
				p.log.Info("restart closure: add consumer region",
					"round", round, "edge", key,
					"producer_region", pr, "consumer_region", cr)
			}

			// 规则三：集合内区域消费的阻塞结果不可用且生产者已完成
			// （即被标记丢失）时拉入生产者区域；生产者仍在运行则不加入。
			if inSet[cr] && !inSet[pr] {
				switch {
				case !p.completed[producer]:
					p.log.Info("restart closure: producer still running, not added",
						"round", round, "edge", key, "producer_region", pr)
				case !p.lost[key]:
					// 生产者已完成且未丢失：结果仍可用，无需重启生产者。
				default:
					inSet[pr] = true
					addedCount++
					p.log.Info("restart closure: add producer region (result lost)",
						"round", round, "edge", key,
						"producer_region", pr, "consumer_region", cr)
				}
			}
		}
		if addedCount == 0 {
			p.log.Info("restart closure: converged", "rounds", round, "regions", sortedKeys(inSet))
			break
		}
	}

	regionList := make([]int, 0, len(inSet))
	taskSet := make(map[int]bool)
	for r := range inSet {
		regionList = append(regionList, r)
		for _, t := range p.regionTask[r] {
			taskSet[t] = true
		}
	}
	sort.Ints(regionList)
	taskList := make([]int, 0, len(taskSet))
	for t := range taskSet {
		taskList = append(taskList, t)
	}
	sort.Ints(taskList)

	plan := RestartPlan{Regions: regionList, Tasks: taskList}
	p.log.Info("restart closure: result",
		"failed_task", failedTask, "regions", plan.Regions, "tasks", plan.Tasks)
	return plan
}

// regionGraphHasCycle 以阻塞边构成的区域有向图检测成环（DFS 着色）。
func (p *Planner) regionGraphHasCycle() bool {
	adj := make(map[int]map[int]bool)
	for key := range p.allEdges {
		if p.allEdges[key] != Blocking {
			continue
		}
		pr, cr := p.taskRegion[key[0]], p.taskRegion[key[1]]
		if adj[pr] == nil {
			adj[pr] = make(map[int]bool)
		}
		adj[pr][cr] = true
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[int]int)
	var dfs func(int) bool
	dfs = func(r int) bool {
		color[r] = gray
		next := make([]int, 0, len(adj[r]))
		for c := range adj[r] {
			next = append(next, c)
		}
		sort.Ints(next)
		for _, c := range next {
			switch color[c] {
			case gray:
				return true
			case white:
				if dfs(c) {
					return true
				}
			}
		}
		color[r] = black
		return false
	}
	for _, r := range p.regions {
		if color[r] == white && dfs(r) {
			return true
		}
	}
	return false
}

func sortedKeys(m map[int]bool) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func edgeLogList(edges []Edge) []edgeLog {
	out := make([]edgeLog, 0, len(edges))
	for _, e := range edges {
		out = append(out, edgeLog{From: e.From, To: e.To, Kind: kindName(e.Kind)})
	}
	return out
}

type edgeLog struct {
	From int    `json:"from"`
	To   int    `json:"to"`
	Kind string `json:"kind"`
}

func kindName(k EdgeKind) string {
	if k == Blocking {
		return "blocking"
	}
	return "pipeline"
}
