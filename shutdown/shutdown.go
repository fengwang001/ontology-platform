// Package shutdown 实现带宽限期与强杀升级的服务有序终止编排器。
//
// 依赖方向约定：A 依赖 B 表示 A 必须先于 B 停止（B 是 A 的被依赖者）。
// 开始关停时刻为 T0，此刻没有存活依赖者的服务立即收到终止信号；
// 其余服务在其全部依赖者停止的最晚时刻收到终止信号。收到信号后
// 宽限期内可自行退出，到期未退出则在 终止时刻+宽限期 被强杀。
package shutdown

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrNonPositiveGrace   = errors.New("shutdown: 宽限期必须为正整数毫秒")
	ErrDuplicateService   = errors.New("shutdown: 服务标识重复")
	ErrUnknownDependency  = errors.New("shutdown: 依赖了未注册的服务")
	ErrDependencyCycle    = errors.New("shutdown: 依赖关系成环")
	ErrRegisterAfterStart = errors.New("shutdown: 开始关停后不允许再注册服务")
	ErrAlreadyStarted     = errors.New("shutdown: 关停已开始，不允许重复开始")
	ErrTimeRegression     = errors.New("shutdown: 时刻早于此前见过的任一时刻")
	ErrServiceNotFound    = errors.New("shutdown: 服务不存在")
	ErrAlreadyStopped     = errors.New("shutdown: 服务已停止")
	ErrNotTerminated      = errors.New("shutdown: 服务尚未收到终止信号")
)

// StopMethod 表示服务的停止方式。
type StopMethod int

const (
	// NotStopped 表示服务尚未停止。
	NotStopped StopMethod = iota
	// Exited 表示服务在宽限期内自行退出。
	Exited
	// Killed 表示服务超过宽限期被强杀。
	Killed
)

func (m StopMethod) String() string {
	switch m {
	case Exited:
		return "exited"
	case Killed:
		return "killed"
	default:
		return "not-stopped"
	}
}

// Status 是服务在某一结算时刻下的可查询状态。
type Status struct {
	ID           string
	Terminated   bool
	TerminatedAt int64
	Stopped      bool
	StoppedAt    int64
	Method       StopMethod
}

// EventKind 表示编排器记录的事件类型。
type EventKind int

const (
	EventTerminated EventKind = iota
	EventExited
	EventKilled
)

func (k EventKind) String() string {
	switch k {
	case EventTerminated:
		return "terminated"
	case EventExited:
		return "exited"
	case EventKilled:
		return "killed"
	default:
		return "unknown"
	}
}

// Event 记录一次终止信号下发或停止结算，按结算顺序追加。
type Event struct {
	Time int64
	ID   string
	Kind EventKind
}

// service 是单个服务的内部状态。
type service struct {
	id           string
	grace        int64
	deps         []string // 去重后的依赖（我依赖谁）
	dependents   []string // 反向边（谁依赖我），按注册顺序
	index        int      // 注册序号，用于同时刻强杀的确定性排序
	terminated   bool
	terminatedAt int64
	stopped      bool
	stoppedAt    int64
	method       StopMethod
}

// Orchestrator 是服务有序终止编排器，所有方法可并发调用，
// 内部以互斥锁串行化，结果等价于某个串行顺序。
type Orchestrator struct {
	mu       sync.Mutex
	order    []string // 注册顺序
	services map[string]*service
	started  bool
	t0       int64
	maxSeen  int64
	hasTime  bool
	events   []Event
}

// New 创建一个空的编排器。
func New() *Orchestrator {
	return &Orchestrator{services: make(map[string]*service)}
}

// Register 注册服务：给定标识、正整数毫秒宽限期与所依赖的已注册服务。
func (o *Orchestrator) Register(now int64, id string, graceMs int64, deps []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.advanceLocked(now); err != nil {
		return err
	}
	if graceMs <= 0 {
		return ErrNonPositiveGrace
	}
	if o.started {
		return ErrRegisterAfterStart
	}
	if _, ok := o.services[id]; ok {
		return ErrDuplicateService
	}
	seen := make(map[string]bool, len(deps))
	uniq := make([]string, 0, len(deps))
	for _, d := range deps {
		if d == id {
			return ErrDependencyCycle
		}
		if _, ok := o.services[d]; !ok {
			return ErrUnknownDependency
		}
		if !seen[d] {
			seen[d] = true
			uniq = append(uniq, d)
		}
	}
	if o.reachesLocked(uniq, id) {
		return ErrDependencyCycle
	}
	s := &service{id: id, grace: graceMs, deps: uniq, index: len(o.order)}
	o.services[id] = s
	o.order = append(o.order, id)
	for _, d := range uniq {
		dep := o.services[d]
		dep.dependents = append(dep.dependents, id)
	}
	return nil
}

// Start 在 T0=now 开始关停：没有存活依赖者的服务立即收到终止信号。
func (o *Orchestrator) Start(now int64) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.advanceLocked(now); err != nil {
		return err
	}
	if o.started {
		return ErrAlreadyStarted
	}
	o.started = true
	o.t0 = now
	for _, id := range o.order {
		s := o.services[id]
		if len(s.dependents) == 0 {
			o.terminateLocked(s, now)
		}
	}
	o.settleLocked(now)
	return nil
}

// ReportExit 上报服务在 now 时刻自行退出。
func (o *Orchestrator) ReportExit(now int64, id string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.advanceLocked(now); err != nil {
		return err
	}
	o.settleLocked(now)
	s, ok := o.services[id]
	if !ok {
		return ErrServiceNotFound
	}
	if s.stopped {
		return ErrAlreadyStopped
	}
	if !s.terminated {
		return ErrNotTerminated
	}
	o.stopLocked(s, now, Exited)
	return nil
}

// Query 查询服务在 now 时刻结算后的状态。
func (o *Orchestrator) Query(now int64, id string) (Status, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.advanceLocked(now); err != nil {
		return Status{}, err
	}
	o.settleLocked(now)
	s, ok := o.services[id]
	if !ok {
		return Status{}, ErrServiceNotFound
	}
	return Status{
		ID:           s.id,
		Terminated:   s.terminated,
		TerminatedAt: s.terminatedAt,
		Stopped:      s.stopped,
		StoppedAt:    s.stoppedAt,
		Method:       s.method,
	}, nil
}

// Events 返回截至当前已结算的事件序列副本。
func (o *Orchestrator) Events() []Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Event, len(o.events))
	copy(out, o.events)
	return out
}

// Snapshot 返回 now 时刻结算后全部服务的状态，按注册顺序排列。
func (o *Orchestrator) Snapshot(now int64) ([]Status, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.advanceLocked(now); err != nil {
		return nil, err
	}
	o.settleLocked(now)
	out := make([]Status, 0, len(o.order))
	for _, id := range o.order {
		s := o.services[id]
		out = append(out, Status{
			ID:           s.id,
			Terminated:   s.terminated,
			TerminatedAt: s.terminatedAt,
			Stopped:      s.stopped,
			StoppedAt:    s.stoppedAt,
			Method:       s.method,
		})
	}
	return out, nil
}

// advanceLocked 校验并推进单调时钟：任何调用的时刻都不得早于此前见过的任一时刻。
func (o *Orchestrator) advanceLocked(now int64) error {
	if o.hasTime && now < o.maxSeen {
		return ErrTimeRegression
	}
	o.maxSeen = now
	o.hasTime = true
	return nil
}

// reachesLocked 检测从 starts 出发沿依赖边能否到达 target（通用成环检测）。
func (o *Orchestrator) reachesLocked(starts []string, target string) bool {
	visited := make(map[string]bool)
	stack := append([]string(nil), starts...)
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur == target {
			return true
		}
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if s, ok := o.services[cur]; ok {
			stack = append(stack, s.deps...)
		}
	}
	return false
}

// terminateLocked 在 at 时刻向服务下发终止信号并记录事件。
func (o *Orchestrator) terminateLocked(s *service, at int64) {
	s.terminated = true
	s.terminatedAt = at
	o.events = append(o.events, Event{Time: at, ID: s.id, Kind: EventTerminated})
}

// stopLocked 在 at 时刻以 method 停止服务，并级联终止其全部依赖者
// 都已停止的被依赖服务（终止时刻取依赖者停止时刻的最大值）。
func (o *Orchestrator) stopLocked(s *service, at int64, method StopMethod) {
	s.stopped = true
	s.stoppedAt = at
	s.method = method
	kind := EventExited
	if method == Killed {
		kind = EventKilled
	}
	o.events = append(o.events, Event{Time: at, ID: s.id, Kind: kind})
	for _, depID := range s.deps {
		dep := o.services[depID]
		if dep.terminated {
			continue
		}
		allStopped := true
		var maxStop int64
		for _, d := range dep.dependents {
			ds := o.services[d]
			if !ds.stopped {
				allStopped = false
				break
			}
			if ds.stoppedAt > maxStop {
				maxStop = ds.stoppedAt
			}
		}
		if allStopped {
			o.terminateLocked(dep, maxStop)
		}
	}
}

// settleLocked 把截至 now 所有到期的强杀按 (到期时刻, 注册序号) 依次结算；
// 强杀引起的下游终止若产生新的到期强杀，也在同一调用内按时间先后级联结算。
func (o *Orchestrator) settleLocked(now int64) {
	for {
		var victim *service
		var due int64
		for _, id := range o.order {
			s := o.services[id]
			if !s.terminated || s.stopped {
				continue
			}
			d := s.terminatedAt + s.grace
			if d > now {
				continue
			}
			if victim == nil || d < due || (d == due && s.index < victim.index) {
				victim, due = s, d
			}
		}
		if victim == nil {
			return
		}
		o.stopLocked(victim, due, Killed)
	}
}
