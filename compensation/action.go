package compensation

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// StepSpec 声明分支内的一个顺序子操作。
//
// 每个子操作同时给出正向操作与一个逆操作工厂：只有 Apply 成功
// 之后，MakeInverse 才会被调用，其产物登记到分支的"已生效栈"。
type StepSpec struct {
	Op          Operation
	MakeInverse func() Inverse
}

// BranchSpec 声明一条副作用分支及其依赖。
//
// DependsOn 中的每个名称必须是同一个动作内其他分支的名字，
// 语义为：本分支必须等到这些分支全部完全生效后才能开始推进。
type BranchSpec struct {
	Name      string
	DependsOn []string
	Steps     []StepSpec
}

// appliedStep 记录一条已生效子操作，供补偿阶段按逆序撤销。
type appliedStep struct {
	index int
	inv   Inverse
	name  string
}

type branchState struct {
	spec *BranchSpec

	// 正向阶段。
	outcomeMu      sync.RWMutex
	started        bool
	succeeded      bool
	applied        []appliedStep // 按生效顺序保存（wg.Wait 后只读）
	failKind       ErrorKind     // 0 / KindOperationFailure / KindUpstreamFailure
	failStep       int
	failCause      error
	upstreamSource string

	// 补偿阶段。
	compMu      sync.Mutex
	compensated bool
	compRunning bool
	undoErrors  []*BranchError
}

// Action 是一次已通过声明校验的动作及其全部运行期状态。
type Action struct {
	id     uint64
	name   string
	graph  *Graph
	locks  *LockManager
	logger Logger

	branches   map[string]*branchState
	order      []string            // 声明顺序，保证遍历稳定
	downstream map[string][]string // branch -> 直接依赖它的分支

	// remainingDownstream 是补偿顺序门禁的核心：
	// 对分支 b，其值 = 依赖 b 但尚未完成补偿的直接下游分支数。
	// 该值归零时 b 才允许开始补偿；门禁判断只读 b 自己这一个计数，
	// 开销为 O(1)，不遍历分支集合。
	remainingDownstream map[string]*int32
	gateMu              sync.Mutex
	gateChanged         map[string]chan struct{} // 补偿门禁计数变化广播
	doneCh              map[string]chan struct{} // 正向结果落定广播
	compDoneCh          map[string]chan struct{} // 补偿完成广播

	execDone bool
	report   *Report
	token    *Token
}

var nextActionID uint64

// Executor 在共享对象图与锁管理器上声明、运行动作。
type Executor struct {
	Graph  *Graph
	Locks  *LockManager
	Logger Logger
}

// NewExecutor 创建执行器；locks 为 nil 时使用独立的新锁管理器
// （此时跨动作串行化只在该执行器内部成立）。
func NewExecutor(g *Graph, locks *LockManager, logger Logger) *Executor {
	if g == nil {
		g = NewGraph()
	}
	if locks == nil {
		locks = NewLockManager()
	}
	return &Executor{Graph: g, Locks: locks, Logger: logger}
}

// Declare 校验并创建一个动作。
//
// 声明阶段完成：分支名唯一性、依赖引用合法性、依赖环检测。
// 任何一项失败都在触碰对象图/获取任何锁之前返回，因此被拒绝的
// 声明不会产生任何可观察的对象图改动。
func (e *Executor) Declare(name string, branches []BranchSpec) (*Action, error) {
	a := &Action{
		id:                  atomic.AddUint64(&nextActionID, 1),
		name:                name,
		graph:               e.Graph,
		locks:               e.Locks,
		logger:              e.Logger,
		branches:            make(map[string]*branchState),
		downstream:          make(map[string][]string),
		remainingDownstream: make(map[string]*int32),
		gateChanged:         make(map[string]chan struct{}),
		doneCh:              make(map[string]chan struct{}),
		compDoneCh:          make(map[string]chan struct{}),
	}

	for i := range branches {
		b := &branches[i]
		if b.Name == "" {
			return nil, fmt.Errorf("branch at index %d has empty name", i)
		}
		if _, dup := a.branches[b.Name]; dup {
			return nil, fmt.Errorf("duplicate branch name %q", b.Name)
		}
		a.branches[b.Name] = &branchState{spec: b, failStep: -1}
		a.order = append(a.order, b.Name)
	}

	for _, bname := range a.order {
		seen := make(map[string]bool)
		for _, dep := range a.branches[bname].spec.DependsOn {
			if dep == bname {
				return nil, &cycleError{cycle: []string{bname, bname}}
			}
			if _, ok := a.branches[dep]; !ok {
				return nil, fmt.Errorf("branch %q depends on unknown branch %q", bname, dep)
			}
			if seen[dep] {
				return nil, fmt.Errorf("branch %q declares duplicate dependency %q", bname, dep)
			}
			seen[dep] = true
		}
	}

	if cyc := detectCycle(a.branches, a.order); cyc != nil {
		return nil, &cycleError{cycle: cyc}
	}

	// 构建逆向邻接表与初始门禁计数（不涉及任何副作用）。
	for _, bname := range a.order {
		n := int32(0)
		a.remainingDownstream[bname] = &n
		a.gateChanged[bname] = make(chan struct{})
		a.doneCh[bname] = make(chan struct{})
		a.compDoneCh[bname] = make(chan struct{})
	}
	for _, bname := range a.order {
		for _, dep := range a.branches[bname].spec.DependsOn {
			a.downstream[dep] = append(a.downstream[dep], bname)
			atomic.AddInt32(a.remainingDownstream[dep], 1)
		}
	}
	return a, nil
}

// detectCycle 用 DFS 三色标记在依赖图上找环。
// 依赖边方向为 branch -> 它依赖的上游分支；找到后环以
// "起点 ... 回到起点" 的名称序列返回。返回 nil 表示无环。
func detectCycle(branches map[string]*branchState, order []string) []string {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(branches))
	var stack []string
	var dfs func(string) []string
	dfs = func(u string) []string {
		color[u] = gray
		stack = append(stack, u)
		for _, v := range branches[u].spec.DependsOn {
			switch color[v] {
			case white:
				if cyc := dfs(v); cyc != nil {
					return cyc
				}
			case gray:
				idx := indexOf(stack, v)
				cyc := append([]string(nil), stack[idx:]...)
				return append(cyc, v)
			}
		}
		stack = stack[:len(stack)-1]
		color[u] = black
		return nil
	}
	for _, name := range order {
		if color[name] == white {
			if cyc := dfs(name); cyc != nil {
				return cyc
			}
		}
	}
	return nil
}

func indexOf(xs []string, x string) int {
	for i, v := range xs {
		if v == x {
			return i
		}
	}
	return -1
}

// Execute 运行正向阶段：无依赖关系的分支并发推进，有依赖的分支
// 等待上游全部生效；任一子操作失败沿依赖链向下游传播，未开始的
// 下游子操作永远不会开始。
//
// 若存在任意失败，Execute 会自动按逆向依赖拓扑展开补偿，并在
// 补偿结束（含逆操作失败）后释放动作持有的对象图键锁。
// 全部成功时调用方负责在用完后调用 Release 释放锁。
func (a *Action) Execute() *Report {
	keys := a.allKeys()
	a.token = a.locks.LockAll(a.id, keys)

	a.runForward()

	rep := &Report{}
	a.collectForwardErrors(rep)
	if len(rep.Errors) > 0 {
		a.compensateAll(rep)
		a.report = rep
		a.execDone = true
		a.locks.UnlockAll(a.token)
		a.token = nil
	}
	return nilOrReport(rep)
}

// Release 释放正向全部成功的动作持有的键锁。失败动作的锁已在
// Execute 内部释放，重复调用是安全的空操作。
func (a *Action) Release() {
	if a.token != nil {
		a.locks.UnlockAll(a.token)
		a.token = nil
	}
}

func (a *Action) allKeys() []string {
	var keys []string
	for _, bname := range a.order {
		for _, st := range a.branches[bname].spec.Steps {
			keys = append(keys, st.Op.Keys()...)
			if st.MakeInverse != nil {
				keys = append(keys, st.MakeInverse().Keys()...)
			}
		}
	}
	return keys
}

// runForward 并发调度所有分支。
func (a *Action) runForward() {
	var wg sync.WaitGroup
	for _, bname := range a.order {
		wg.Add(1)
		go func(name string) {
			defer close(a.doneCh[name])
			a.runBranchForward(name, &wg)
		}(bname)
	}
	wg.Wait()
}

// runBranchForward 运行单条分支的正向逻辑；等待全部上游成功，
// 任一上游失败则本分支被动失败且不启动任何子操作。
func (a *Action) runBranchForward(bname string, wg *sync.WaitGroup) {
	defer wg.Done()
	st := a.branches[bname]

	for _, dep := range st.spec.DependsOn {
		up := a.branches[dep]
		if !a.waitBranchOutcome(up) {
			st.outcomeMu.Lock()
			st.failKind = KindUpstreamFailure
			st.upstreamSource = dep
			st.outcomeMu.Unlock()
			return
		}
	}

	st.outcomeMu.Lock()
	st.started = true
	st.outcomeMu.Unlock()
	for i, step := range st.spec.Steps {
		if err := step.Op.Apply(a.graph); err != nil {
			st.outcomeMu.Lock()
			st.failKind = KindOperationFailure
			st.failStep = i
			st.failCause = err
			st.outcomeMu.Unlock()
			return
		}
		var inv Inverse
		if step.MakeInverse != nil {
			inv = step.MakeInverse()
		}
		st.applied = append(st.applied, appliedStep{
			index: i,
			inv:   inv,
			name:  step.Op.Name(),
		})
	}
	st.outcomeMu.Lock()
	st.succeeded = true
	st.outcomeMu.Unlock()
}

// branchDoneCh 返回分支正向结果落定的广播 channel。
func (a *Action) branchDoneCh(st *branchState) chan struct{} {
	a.gateMu.Lock()
	defer a.gateMu.Unlock()
	return a.doneCh[st.spec.Name]
}

// waitBranchOutcome 阻塞等待上游分支正向结束，返回它是否完全成功。
func (a *Action) waitBranchOutcome(up *branchState) bool {
	ch := a.branchDoneCh(up)
	up.outcomeMu.RLock()
	done := up.succeeded || up.failKind != 0
	ok := up.succeeded
	up.outcomeMu.RUnlock()
	if done {
		return ok
	}
	<-ch
	up.outcomeMu.RLock()
	ok = up.succeeded
	up.outcomeMu.RUnlock()
	return ok
}

func (a *Action) collectForwardErrors(rep *Report) {
	for _, bname := range a.order {
		st := a.branches[bname]
		st.outcomeMu.RLock()
		kind := st.failKind
		step := st.failStep
		cause := st.failCause
		upstream := st.upstreamSource
		st.outcomeMu.RUnlock()
		switch kind {
		case KindOperationFailure:
			rep.Errors = append(rep.Errors, &BranchError{
				Kind:       KindOperationFailure,
				BranchName: bname,
				StepIndex:  step,
				Cause:      cause,
			})
		case KindUpstreamFailure:
			rep.Errors = append(rep.Errors, &BranchError{
				Kind:       KindUpstreamFailure,
				BranchName: bname,
				StepIndex:  -1,
				Detail:     "upstream=" + upstream,
			})
		}
	}
}

func nilOrReport(rep *Report) *Report {
	if len(rep.Errors) == 0 {
		return nil
	}
	return rep
}
