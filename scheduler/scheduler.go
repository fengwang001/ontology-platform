// Package scheduler 实现抢占式（Spot）实例回收预警下的任务排空与重调度器。
package scheduler

import (
	"errors"
	"sort"
	"sync"
)

// NodeKind 表示节点计费类型。
type NodeKind int

const (
	// Spot 为抢占式节点，可能收到回收通知。
	Spot NodeKind = iota
	// OnDemand 为按需节点，按剩余工作量计费且不会被回收。
	OnDemand
)

func (k NodeKind) String() string {
	switch k {
	case Spot:
		return "Spot"
	case OnDemand:
		return "OnDemand"
	default:
		return "Unknown"
	}
}

// TaskState 为任务三态之一。
type TaskState int

const (
	// Pending 待放置。
	Pending TaskState = iota
	// Running 已放置并运行于某节点。
	Running
	// Done 已完成。
	Done
)

func (s TaskState) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Running:
		return "Running"
	case Done:
		return "Done"
	default:
		return "Unknown"
	}
}

// Node 是调度器内部维护的节点状态。
type Node struct {
	ID       int64
	Kind     NodeKind
	Slots    int64
	used     int64
	notified bool
	dl       int64
}

// Free 返回当前空闲槽位数。
func (n *Node) Free() int64 { return n.Slots - n.used }

// Notified 返回节点是否已收到回收通知。
func (n *Node) Notified() bool { return n.notified }

// Deadline 返回回收截止时间 now+G；未通知时为 0。
func (n *Node) Deadline() int64 { return n.dl }

// Task 是调度器内部维护的任务状态。
type Task struct {
	ID   int64
	Prio int64
	W    int64
	Iv   int64
	Ck   int64

	p    int64
	cp   int64
	rs   int64
	st   TaskState
	node int64
}

// State 返回任务当前状态。
func (t *Task) State() TaskState { return t.st }

// Progress 返回当前进度 p。
func (t *Task) Progress() int64 { return t.p }

// Checkpoint 返回已保存检查点 cp。
func (t *Task) Checkpoint() int64 { return t.cp }

// Restarts 返回任务回待放置的次数 rs。
func (t *Task) Restarts() int64 { return t.rs }

// NodeID 返回任务当前运行所在节点 id；非运行态为 0。
func (t *Task) NodeID() int64 {
	if t.st != Running {
		return 0
	}
	return t.node
}

// Scheduler 是线程安全的任务排空与重调度器。
type Scheduler struct {
	mu    sync.Mutex
	g     int64
	pod   int64
	bud   int64
	nodes map[int64]*Node
	tasks map[int64]*Task
}

var (
	// ErrInvalidConfig 构造参数越界。
	ErrInvalidConfig = errors.New("scheduler: invalid config")
	// ErrInvalidArgument 操作参数非法。
	ErrInvalidArgument = errors.New("scheduler: invalid argument")
	// ErrExists id 已存在。
	ErrExists = errors.New("scheduler: already exists")
	// ErrNotFound 节点或任务不存在。
	ErrNotFound = errors.New("scheduler: not found")
	// ErrNotSpot 目标节点不是 Spot。
	ErrNotSpot = errors.New("scheduler: node is not Spot")
	// ErrNotified 节点已被通知回收。
	ErrNotified = errors.New("scheduler: node already notified")
	// ErrNotNotified 节点尚未收到回收通知。
	ErrNotNotified = errors.New("scheduler: node not notified")
	// ErrNotDue 回收截止时间尚未到达。
	ErrNotDue = errors.New("scheduler: node not due")
	// ErrNotRunning 任务未处于运行态。
	ErrNotRunning = errors.New("scheduler: task not running")
)

// NoticeResult 是一次 Notice 的排空计划结果，三个列表均按 id 升序。
type NoticeResult struct {
	Completed []int64
	Saved     []int64
	Discarded []int64
}

// ExpireItem 描述一个在 Expire 时回到待放置的任务及其返工量。
type ExpireItem struct {
	TaskID int64
	Rework int64
}

// New 以宽限期 G、按需单价 Pod、按需预算 Bud 构造调度器；越界整体拒绝。
func New(grace, onDemandPrice, budget int64) (*Scheduler, error) {
	if grace < 1 || grace > 1_000_000 ||
		onDemandPrice < 1 || onDemandPrice > 1_000_000 ||
		budget < 0 || budget > 1_000_000_000_000 {
		return nil, ErrInvalidConfig
	}
	return &Scheduler{
		g:     grace,
		pod:   onDemandPrice,
		bud:   budget,
		nodes: make(map[int64]*Node),
		tasks: make(map[int64]*Task),
	}, nil
}

// AddNode 添加节点。
func (s *Scheduler) AddNode(id int64, kind NodeKind, slots int64) error {
	if id < 1 || (kind != Spot && kind != OnDemand) || slots < 1 || slots > 1000 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.nodes[id]; ok {
		return ErrExists
	}
	s.nodes[id] = &Node{ID: id, Kind: kind, Slots: slots}
	return nil
}

// AddTask 添加待放置任务。
func (s *Scheduler) AddTask(id, prio, w, iv, ck int64) error {
	const maxV = 1_000_000_000
	if id < 1 || prio < 0 || prio > 255 ||
		w < 1 || w > maxV || iv < 1 || iv > maxV || ck < 1 || ck > maxV {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.tasks[id]; ok {
		return ErrExists
	}
	s.tasks[id] = &Task{ID: id, Prio: prio, W: w, Iv: iv, Ck: ck, st: Pending}
	return nil
}

// Place 对待放置任务执行一轮放置。
func (s *Scheduler) Place() {
	s.mu.Lock()
	defer s.mu.Unlock()

	pending := make([]*Task, 0)
	for _, t := range s.tasks {
		if t.st == Pending {
			pending = append(pending, t)
		}
	}
	sort.Slice(pending, func(i, j int) bool {
		if pending[i].Prio != pending[j].Prio {
			return pending[i].Prio > pending[j].Prio
		}
		return pending[i].ID < pending[j].ID
	})

	for _, t := range pending {
		var target *Node
		if t.rs < 2 {
			target = s.bestSpot()
		}
		if target == nil {
			target = s.bestOnDemand(t)
		}
		if target == nil {
			continue
		}
		target.used++
		t.st = Running
		t.node = target.ID
	}
}

// bestSpot 选取空闲槽最多（并列 id 最小）的未通知 Spot 节点。
func (s *Scheduler) bestSpot() *Node {
	var best *Node
	for _, n := range s.nodes {
		if n.Kind != Spot || n.notified || n.Free() <= 0 {
			continue
		}
		if best == nil || n.Free() > best.Free() ||
			(n.Free() == best.Free() && n.ID < best.ID) {
			best = n
		}
	}
	return best
}

// bestOnDemand 选取 id 最小且预算充足的空闲 OnDemand 节点；成功即扣预算。
func (s *Scheduler) bestOnDemand(t *Task) *Node {
	var best *Node
	for _, n := range s.nodes {
		if n.Kind != OnDemand || n.Free() <= 0 {
			continue
		}
		if best == nil || n.ID < best.ID {
			best = n
		}
	}
	if best == nil {
		return nil
	}
	cost := (t.W - t.cp) * s.pod
	if cost > s.bud {
		return nil
	}
	s.bud -= cost
	return best
}

// Report 上报任务进度并推进检查点。
func (s *Scheduler) Report(id, pp int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return ErrNotFound
	}
	if t.st != Running {
		return ErrNotRunning
	}
	if pp < t.p || pp > t.W {
		return ErrInvalidArgument
	}
	t.p = pp
	if newCP := pp / t.Iv * t.Iv; newCP > t.cp {
		t.cp = newCP
	}
	if pp == t.W {
		n := s.nodes[t.node]
		n.used--
		t.st = Done
		t.node = 0
	}
	return nil
}

// Notice 对指定 Spot 节点发出回收通知并生成排空计划。
func (s *Scheduler) Notice(n, now int64) (NoticeResult, error) {
	if now < 0 || now > 1_000_000_000_000_000_000 {
		return NoticeResult{}, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[n]
	if !ok {
		return NoticeResult{}, ErrNotFound
	}
	if node.Kind != Spot {
		return NoticeResult{}, ErrNotSpot
	}
	if node.notified {
		return NoticeResult{}, ErrNotified
	}

	node.notified = true
	node.dl = now + s.g

	res := NoticeResult{}
	var needCk []*Task
	for _, t := range s.tasks {
		if t.st != Running || t.node != node.ID {
			continue
		}
		if t.W-t.p <= s.g {
			res.Completed = append(res.Completed, t.ID)
		} else if t.p-t.cp == 0 {
			res.Saved = append(res.Saved, t.ID)
		} else {
			needCk = append(needCk, t)
		}
	}
	sort.Slice(needCk, func(i, j int) bool {
		if needCk[i].p-needCk[i].cp != needCk[j].p-needCk[j].cp {
			return needCk[i].p-needCk[i].cp > needCk[j].p-needCk[j].cp
		}
		return needCk[i].ID < needCk[j].ID
	})

	var elapsed int64
	stopped := false
	for _, t := range needCk {
		if !stopped && elapsed+t.Ck <= s.g {
			elapsed += t.Ck
			t.cp = t.p
			res.Saved = append(res.Saved, t.ID)
		} else {
			stopped = true
			res.Discarded = append(res.Discarded, t.ID)
		}
	}
	sort.Slice(res.Completed, func(i, j int) bool { return res.Completed[i] < res.Completed[j] })
	sort.Slice(res.Saved, func(i, j int) bool { return res.Saved[i] < res.Saved[j] })
	sort.Slice(res.Discarded, func(i, j int) bool { return res.Discarded[i] < res.Discarded[j] })
	return res, nil
}

// Expire 移除已到期节点，未完成任务回待放置并返回返工量。
func (s *Scheduler) Expire(n, now int64) ([]ExpireItem, error) {
	if now < 0 || now > 1_000_000_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.nodes[n]
	if !ok {
		return nil, ErrNotFound
	}
	if !node.notified {
		return nil, ErrNotNotified
	}
	if now < node.dl {
		return nil, ErrNotDue
	}

	items := make([]ExpireItem, 0)
	for _, t := range s.tasks {
		if t.st != Running || t.node != node.ID {
			continue
		}
		rework := t.p - t.cp
		items = append(items, ExpireItem{TaskID: t.ID, Rework: rework})
		t.rs++
		t.p = t.cp
		t.st = Pending
		t.node = 0
	}
	sort.Slice(items, func(i, j int) bool { return items[i].TaskID < items[j].TaskID })
	delete(s.nodes, node.ID)
	return items, nil
}

// Budget 返回剩余按需预算。
func (s *Scheduler) Budget() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bud
}
