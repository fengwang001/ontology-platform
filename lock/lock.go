// Package lock 在 ceil 之上实现 PCP 授予/阻塞判定；系统天花板与有效优先级增量维护。
package lock

import (
	"errors"
	"sync"

	"ontology/ceil"
)

// 四类互不相同的哨兵错误：被拒操作整体失败且不留状态痕迹。
var ErrUnknownTask = errors.New("lock: task is not registered")
var ErrUnknownResource = errors.New("lock: resource is not registered")
var ErrAlreadyHeld = errors.New("lock: task already holds the resource")
var ErrNotHeld = errors.New("lock: task does not hold the resource")

type Manager struct {
	mu           sync.Mutex
	tasks        map[string]int         // task -> 基础优先级
	tab          *ceil.Table            // 资源天花板表
	holder       map[string]string      // res -> 当前持有者
	ceilCnt      map[int]int            // 全局：天花板值 -> 已持有资源数
	ownCnt       map[string]map[int]int // 每任务：天花板值 -> 持有数
	top, checked int                    // 系统天花板；非导出的 Acquire 检查资源数（恒 0）
}

func NewManager() *Manager {
	return &Manager{tasks: map[string]int{}, tab: ceil.NewTable(), holder: map[string]string{},
		ceilCnt: map[int]int{}, ownCnt: map[string]map[int]int{}}
}
func (m *Manager) AddTask(id string, priority int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		m.tasks[id], m.ownCnt[id] = priority, map[int]int{}
	}
}
func (m *Manager) AddResource(n string) { m.mu.Lock(); defer m.mu.Unlock(); m.tab.AddResource(n) }
func (m *Manager) check(task, res string) (int, error) {
	prio, ok := m.tasks[task]
	if !ok {
		return 0, ErrUnknownTask
	}
	if !m.tab.HasResource(res) {
		return 0, ErrUnknownResource
	}
	return prio, nil
}
func (m *Manager) Use(task, res string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prio, err := m.check(task, res)
	if err != nil {
		return err
	}
	m.tab.Declare(res, task, prio)
	return nil
}
func (m *Manager) Ceiling(r string) int { m.mu.Lock(); defer m.mu.Unlock(); return m.tab.Ceiling(r) }
func (m *Manager) SystemCeiling() int   { m.mu.Lock(); defer m.mu.Unlock(); return m.top }
func dec(c map[int]int, v int) {
	if c[v]--; c[v] == 0 {
		delete(c, v)
	}
}
func maxKey(c map[int]int) (b int) {
	for v := range c {
		if v > b {
			b = v
		}
	}
	return
}
func (m *Manager) EffectivePriority(task string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t := maxKey(m.ownCnt[task]); t > m.tasks[task] {
		return t
	}
	return m.tasks[task]
}

// othersCeiling 只读增量直方图求「他人持有」资源天花板最大值；
// 仅在本任务独占顶层值时在取值集合上回退，不遍历已持有资源。
func (m *Manager) othersCeiling(task string) int {
	own := m.ownCnt[task]
	top := m.top
	if top == 0 || maxKey(own) < top || m.ceilCnt[top]-own[top] > 0 {
		return top
	}
	best := 0
	for v, c := range m.ceilCnt {
		if c-own[v] > 0 && v > best {
			best = v
		}
	}
	return best
}

// Acquire：priority 严格大于他人持有资源的系统天花板才授予；阻塞返回 (false,nil)。
func (m *Manager) Acquire(task, res string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checked = 0 // 判定只读维护值，检查的已持有资源数恒为 0
	prio, err := m.check(task, res)
	if err != nil {
		return false, err
	}
	switch cur := m.holder[res]; {
	case cur == task:
		return false, ErrAlreadyHeld
	case cur != "":
		return false, nil // 他人持有：阻塞，持有关系不变
	}
	if prio <= m.othersCeiling(task) {
		return false, nil
	}
	c := m.tab.Ceiling(res)
	m.holder[res] = task
	m.ceilCnt[c]++
	m.ownCnt[task][c]++
	if c > m.top {
		m.top = c
	}
	return true, nil
}

// Release 解除持有并重算系统天花板；仅在释放顶层值时扫描取值直方图。
func (m *Manager) Release(task, res string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.check(task, res); err != nil {
		return err
	}
	if m.holder[res] != task {
		return ErrNotHeld
	}
	delete(m.holder, res)
	c := m.tab.Ceiling(res)
	if c == 0 {
		return nil
	}
	dec(m.ceilCnt, c)
	dec(m.ownCnt[task], c)
	if c == m.top {
		m.top = maxKey(m.ceilCnt)
	}
	return nil
}
