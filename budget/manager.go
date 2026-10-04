package budget

import (
	"fmt"
	"math/big"
	"sort"
	"sync"
)

// component 为一个已声明的组件：周期 Π、已分配预算 θ（只增不减，Compact 除外）
// 与组件内任务集（按编号索引，编号组件内唯一）。
type component struct {
	name  string
	pi    int64
	theta int64
	tasks map[string]Task
}

// taskSlice 返回按编号排序的任务切片，保证结果与插入顺序无关。
func (c *component) taskSlice() []Task {
	tasks := make([]Task, 0, len(c.tasks))
	for _, t := range c.tasks {
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
	return tasks
}

// Manager 为分层调度预算管理器。所有方法可并发调用，
// 效果等价于某个串行顺序（内部由一把互斥锁串行化）。
type Manager struct {
	mu         sync.Mutex
	components map[string]*component
}

// NewManager 创建一个空的管理器。
func NewManager() *Manager {
	return &Manager{components: make(map[string]*component)}
}

// Declare 建立一个空组件（θ=0）。仅可能报参数非法、重复、容量已满。
func (m *Manager) Declare(name string, pi int64) error {
	if name == "" || pi < 1 || pi > MaxPeriod {
		return &RejectError{Reason: RejectInvalidParam, Detail: fmt.Sprintf("name=%q pi=%d", name, pi)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.components[name]; exists {
		return &RejectError{Reason: RejectDuplicate, Detail: fmt.Sprintf("component %q exists", name)}
	}
	if len(m.components) >= MaxComponents {
		return &RejectError{Reason: RejectCapacity, Detail: "component limit reached"}
	}
	m.components[name] = &component{name: name, pi: pi, tasks: make(map[string]Task)}
	return nil
}

// AddTask 向组件加入任务并按需提升 θ=max(θ, MinBudget(原任务集+新任务))。
// 全局约束 Σθ/Π<=1（精确有理数比较，恰等于 1 通过）。被拒绝时不改变任何状态。
func (m *Manager) AddTask(name string, task Task) error {
	if name == "" || !task.valid() {
		return &RejectError{Reason: RejectInvalidParam, Detail: fmt.Sprintf("name=%q task=%+v", name, task)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, exists := m.components[name]
	if !exists {
		return &RejectError{Reason: RejectNotFound, Detail: fmt.Sprintf("component %q", name)}
	}
	if _, dup := c.tasks[task.ID]; dup {
		return &RejectError{Reason: RejectDuplicate, Detail: fmt.Sprintf("task %q exists in %q", task.ID, name)}
	}
	if len(c.tasks) >= MaxTasksPerComponent {
		return &RejectError{Reason: RejectCapacity, Detail: "task limit reached"}
	}
	candidate := append(c.taskSlice(), task)
	dmax, h, within := horizon(c.pi, candidate)
	if !within {
		return &RejectError{Reason: RejectTooLarge, Detail: fmt.Sprintf("Dmax+H exceeds %d", MaxHorizon)}
	}
	minTheta, ok, vt := minBudget(c.pi, candidate, dmax, h)
	if !ok {
		return &RejectError{Reason: RejectInfeasible, Detail: "task set infeasible at theta=Pi", Theta: c.pi, ViolationT: vt}
	}
	newTheta := c.theta
	if minTheta > newTheta {
		newTheta = minTheta
	}
	if !m.totalWith(name, newTheta) {
		return &RejectError{Reason: RejectOverload, Detail: "global bandwidth exceeded", Theta: newTheta}
	}
	c.tasks[task.ID] = task
	c.theta = newTheta
	return nil
}

// RemoveTask 删除组件内任务，不改变 θ。
func (m *Manager) RemoveTask(name, taskID string) error {
	if name == "" || taskID == "" {
		return &RejectError{Reason: RejectInvalidParam, Detail: fmt.Sprintf("name=%q task=%q", name, taskID)}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, exists := m.components[name]
	if !exists {
		return &RejectError{Reason: RejectNotFound, Detail: fmt.Sprintf("component %q", name)}
	}
	if _, ok := c.tasks[taskID]; !ok {
		return &RejectError{Reason: RejectNotFound, Detail: fmt.Sprintf("task %q in %q", taskID, name)}
	}
	delete(c.tasks, taskID)
	return nil
}

// Compact 将 θ 重算为当前任务集的 MinBudget（无任务则为 0）并释放带宽。
func (m *Manager) Compact(name string) error {
	if name == "" {
		return &RejectError{Reason: RejectInvalidParam, Detail: "empty name"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, exists := m.components[name]
	if !exists {
		return &RejectError{Reason: RejectNotFound, Detail: fmt.Sprintf("component %q", name)}
	}
	theta, _, _ := MinBudget(c.pi, c.taskSlice())
	c.theta = theta
	return nil
}

// Budget 返回组件当前已分配预算 θ。
func (m *Manager) Budget(name string) (int64, error) {
	if name == "" {
		return 0, &RejectError{Reason: RejectInvalidParam, Detail: "empty name"}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	c, exists := m.components[name]
	if !exists {
		return 0, &RejectError{Reason: RejectNotFound, Detail: fmt.Sprintf("component %q", name)}
	}
	return c.theta, nil
}

// Total 返回 Σθ/Π 的既约分数（分母为正，零表示 0/1）。
func (m *Manager) Total() *big.Rat {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := new(big.Rat)
	for _, c := range m.components {
		total.Add(total, big.NewRat(c.theta, c.pi))
	}
	return total
}

// totalWith 检查将组件 name 的 θ 调整为 newTheta 后，Σθ/Π 是否仍 <=1（精确比较）。
// 调用方须持有 m.mu。
func (m *Manager) totalWith(name string, newTheta int64) bool {
	sum := new(big.Rat)
	for _, c := range m.components {
		theta := c.theta
		if c.name == name {
			theta = newTheta
		}
		sum.Add(sum, big.NewRat(theta, c.pi))
	}
	return sum.Cmp(big.NewRat(1, 1)) <= 0
}
