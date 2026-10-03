package budget

import (
	"math/big"
	"sync"
)

const (
	maxComponents = 8
	maxTasks      = 8
)

type component struct {
	pi    int64
	theta int64
	tasks map[string]Task
	order []string // 保持任务插入顺序的稳定快照；MinBudget 本身与顺序无关
}

// Manager 是分层调度预算管理器。
//
// 所有方法都在同一把 RWMutex 下串行化临界区，因此并发调用的结果
// 等价于某个合法的串行顺序；相同的操作序列重放得到完全相同的
// 预算与拒绝点。
type Manager struct {
	mu sync.RWMutex
	cs map[string]*component
}

func NewManager() *Manager {
	return &Manager{cs: make(map[string]*component)}
}

// Declare 创建一个预算为 0 的空组件。
// 只可能报：参数非法、名称重复、组件容量已满。
func (m *Manager) Declare(name string, pi int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" || !validatePi(pi) {
		return rejectError("Declare", ReasonInvalid,
			"name must be non-empty and 1 <= Pi <= 1000")
	}
	if _, exists := m.cs[name]; exists {
		return rejectError("Declare", ReasonDuplicate, "component name already exists: "+name)
	}
	if len(m.cs) >= maxComponents {
		return rejectError("Declare", ReasonCapacity, "at most 8 components")
	}
	m.cs[name] = &component{pi: pi, tasks: make(map[string]Task)}
	return nil
}

// AddTask 向组件加入任务。被拒时不改变任何状态。
//
// 新预算 theta' = max(theta, MinBudget(原任务集 + 新任务))，
// 已分配预算只增不减；全局要求 sum(theta/Pi) <= 1（恰为 1 通过）。
func (m *Manager) AddTask(name string, task Task) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 拒绝顺序：参数非法 -> 不存在 -> 重复 -> 容量 -> 规模过大 -> 不可行 -> 过载。
	if name == "" || !validateTask(task) {
		return rejectError("AddTask", ReasonInvalid,
			"task id non-empty and 1 <= C <= D <= T <= 1000 required")
	}
	c, ok := m.cs[name]
	if !ok {
		return rejectError("AddTask", ReasonNotFound, "no such component: "+name)
	}
	if _, dup := c.tasks[task.ID]; dup {
		return rejectError("AddTask", ReasonDuplicate, "task id already exists: "+task.ID)
	}
	if len(c.tasks) >= maxTasks {
		return rejectError("AddTask", ReasonCapacity, "at most 8 tasks per component")
	}

	// 在副本上计算 MinBudget，保证拒绝时无半更新。
	candidate := m.snapshotTasksLocked(c)
	candidate = append(candidate, task)
	res := minBudget(c.pi, candidate)
	if res.tooLarge {
		return rejectError("AddTask", ReasonTooLarge, "Dmax+H exceeds 1000000")
	}
	if !res.feasible {
		e := rejectError("AddTask", ReasonInfeasible,
			"task set unschedulable even with Theta=Pi")
		e.ViolateT = res.violateT
		return e
	}

	newTheta := c.theta
	if res.theta > newTheta {
		newTheta = res.theta
	}
	if over, total := m.admissionOverloadedLocked(c, newTheta); over {
		e := rejectError("AddTask", ReasonOverloaded,
			"global bandwidth would exceed 1: "+total.String()+" > 1/1")
		return e
	}

	// 提交：任务与预算同时落地。
	c.tasks[task.ID] = task
	c.order = append(c.order, task.ID)
	c.theta = newTheta
	return nil
}

// RemoveTask 删除任务但不改变已分配预算（预算只增不减，直到 Compact）。
func (m *Manager) RemoveTask(name, taskID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" || taskID == "" {
		return rejectError("RemoveTask", ReasonInvalid, "name and task id must be non-empty")
	}
	c, ok := m.cs[name]
	if !ok {
		return rejectError("RemoveTask", ReasonNotFound, "no such component: "+name)
	}
	if _, has := c.tasks[taskID]; !has {
		return rejectError("RemoveTask", ReasonNotFound, "no such task: "+taskID)
	}
	delete(c.tasks, taskID)
	for i, id := range c.order {
		if id == taskID {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return nil
}

// Compact 把预算重算为当前任务集的 MinBudget（无任务则为 0），释放带宽。
func (m *Manager) Compact(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" {
		return rejectError("Compact", ReasonInvalid, "name must be non-empty")
	}
	c, ok := m.cs[name]
	if !ok {
		return rejectError("Compact", ReasonNotFound, "no such component: "+name)
	}
	tasks := m.snapshotTasksLocked(c)
	var newTheta int64
	if len(tasks) > 0 {
		res := minBudget(c.pi, tasks)
		// 不变式保证：当前任务集是此前被接纳过的子集，且当前 theta >= 其 MinBudget，
		// 因此规模与可行性必然成立；若发生意外，按规则返回且不改变状态。
		if res.tooLarge {
			return rejectError("Compact", ReasonTooLarge, "Dmax+H exceeds 1000000")
		}
		if !res.feasible {
			e := rejectError("Compact", ReasonInfeasible, "task set unschedulable with Theta=Pi")
			e.ViolateT = res.violateT
			return e
		}
		newTheta = res.theta
	}
	c.theta = newTheta
	return nil
}

// Budget 返回组件当前已分配预算 theta。
func (m *Manager) Budget(name string) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name == "" {
		return 0, rejectError("Budget", ReasonInvalid, "name must be non-empty")
	}
	c, ok := m.cs[name]
	if !ok {
		return 0, rejectError("Budget", ReasonNotFound, "no such component: "+name)
	}
	return c.theta, nil
}

// Total 返回全局带宽 sum(theta/Pi) 的既约分数；零为 0/1。
func (m *Manager) Total() Fraction {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sum := new(big.Rat)
	for _, c := range m.cs {
		sum.Add(sum, new(big.Rat).SetFrac(big.NewInt(c.theta), big.NewInt(c.pi)))
	}
	if sum.Sign() == 0 {
		return Fraction{Num: 0, Den: 1}
	}
	return Fraction{Num: sum.Num().Int64(), Den: sum.Denom().Int64()}
}

// Snapshot 返回组件的只读快照，供测试与观察。
func (m *Manager) Snapshot(name string) (Component, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if name == "" {
		return Component{}, rejectError("Snapshot", ReasonInvalid, "name must be non-empty")
	}
	c, ok := m.cs[name]
	if !ok {
		return Component{}, rejectError("Snapshot", ReasonNotFound, "no such component: "+name)
	}
	return Component{Name: name, Pi: c.pi, Theta: c.theta, Tasks: m.snapshotTasksLocked(c)}, nil
}

func (m *Manager) snapshotTasksLocked(c *component) []Task {
	tasks := make([]Task, 0, len(c.order))
	for _, id := range c.order {
		tasks = append(tasks, c.tasks[id])
	}
	return tasks
}

// admissionOverloadedLocked 判断将组件 c 的预算换成 newTheta 后
// 全局带宽是否超过 1。全程 big.Rat 精确比较。
func (m *Manager) admissionOverloadedLocked(self *component, newTheta int64) (bool, Fraction) {
	sum := new(big.Rat)
	for _, c := range m.cs {
		theta := c.theta
		if c == self {
			theta = newTheta
		}
		sum.Add(sum, new(big.Rat).SetFrac(big.NewInt(theta), big.NewInt(c.pi)))
	}
	over := sum.Cmp(new(big.Rat).SetInt64(1)) > 0
	total := Fraction{Num: 0, Den: 1}
	if sum.Sign() != 0 {
		total = Fraction{Num: sum.Num().Int64(), Den: sum.Denom().Int64()}
	}
	return over, total
}
