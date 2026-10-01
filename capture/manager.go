package capture

import "sync"

// variable 是一个被捕获的变量。
//
// 开放（closed == false）时不持有自身存储，所有读写直接作用于值栈槽 slot，
// 同一槽上的多个捕获共享同一个 variable（即同一句柄）。
// 关闭时把当前槽值复制到 storage，此后读写只作用于 storage。
type variable struct {
	id      int // 句柄编号，从 1 起，递增不复用
	slot    int // 捕获时所在的栈槽
	holders int // 持有数，每次捕获加一，每次释放减一
	closed  bool
	storage int
}

// Manager 是闭包捕获变量的开闭管理器。
// 所有方法均可并发调用；单把互斥锁使每个操作原子化，
// 任意并发执行都等价于某个串行顺序（线性一致）。
type Manager struct {
	mu sync.Mutex

	stack []int // 值栈；len(stack) 即栈顶，槽号为 [0, top)

	// open 记录每个栈槽上当前共享的开放变量。
	// 不变式：任何时刻同一槽至多一个键；持有的 variable 一定开放且 holders > 0。
	open map[int]*variable

	// vars 按句柄编号保存变量。
	// holders == 0 表示已释放完：句柄仍可查到（原因可区分为“已释放完”），
	// 但读写与再次释放均被拒绝。
	vars map[int]*variable

	nextID int // 下一个待分配的句柄编号
}

// New 创建空管理器（栈顶为 0，下一句柄为 1）。
func New() *Manager {
	return &Manager{
		open:   make(map[int]*variable),
		vars:   make(map[int]*variable),
		nextID: 1,
	}
}

// Top 返回当前值栈栈顶。
func (m *Manager) Top() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stack)
}

// Push 在栈顶压入 value，返回新槽号（等于压入前栈顶），栈顶加一。
func (m *Manager) Push(value int) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	slot := len(m.stack)
	m.stack = append(m.stack, value)
	return slot
}

// Capture 在槽 slot 上捕获变量，返回句柄。
// 同一槽上已有开放变量则复用同一句柄，否则分配新句柄；每次捕获持有数加一。
// 槽号不小于栈顶（含负槽号）时整体拒绝。
func (m *Manager) Capture(slot int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return 0, newOpError("capture", ReasonCaptureSlotAboveTop)
	}
	if v := m.open[slot]; v != nil {
		v.holders++
		return v.id, nil
	}
	v := &variable{id: m.nextID, slot: slot, holders: 1}
	m.nextID++
	m.open[slot] = v
	m.vars[v.id] = v
	return v.id, nil
}

// SlotGet 直接读取栈槽。槽号越界（负槽号或不小于栈顶）时拒绝。
func (m *Manager) SlotGet(slot int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return 0, newOpError("slot_get", ReasonSlotOutOfRange)
	}
	return m.stack[slot], nil
}

// SlotSet 直接写入栈槽。槽号越界（负槽号或不小于栈顶）时拒绝。
func (m *Manager) SlotSet(slot, value int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return newOpError("slot_set", ReasonSlotOutOfRange)
	}
	m.stack[slot] = value
	return nil
}

// HandleGet 经句柄读取变量：开放时读栈槽，关闭后读自身存储。
// 句柄不存在与句柄已释放完返回互斥的可区分原因。
func (m *Manager) HandleGet(handle int) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.live(handle, "handle_get")
	if err != nil {
		return 0, err
	}
	if v.closed {
		return v.storage, nil
	}
	return m.stack[v.slot], nil
}

// HandleSet 经句柄写入变量：开放时写栈槽（对直接读写栈槽可见），关闭后只写自身存储。
func (m *Manager) HandleSet(handle, value int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.live(handle, "handle_set")
	if err != nil {
		return err
	}
	if v.closed {
		v.storage = value
		return nil
	}
	m.stack[v.slot] = value
	return nil
}

// CloseFrom 从层 level 关闭。
// 对槽号不小于 level 的全部开放变量：把当前槽值复制进自身存储并转为关闭，
// 随后从共享表摘除并把栈顶设为 level。level 恰等于栈顶是合法空操作。
// level 小于 0 或大于栈顶时整体拒绝，不改变任何状态。
func (m *Manager) CloseFrom(level int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	top := len(m.stack)
	if level < 0 || level > top {
		return newOpError("close_from", ReasonCloseLevelOutOfRange)
	}
	for slot, v := range m.open {
		if slot < level {
			continue
		}
		v.storage = m.stack[slot]
		v.closed = true
		delete(m.open, slot)
	}
	m.stack = m.stack[:level]
	return nil
}

// Release 释放一次句柄，持有数减一。
// 减到 0 时若变量仍开放，则从共享表摘除（之后同槽再捕获得到新句柄）。
func (m *Manager) Release(handle int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.live(handle, "release")
	if err != nil {
		return err
	}
	v.holders--
	if v.holders == 0 && !v.closed {
		delete(m.open, v.slot)
	}
	return nil
}

// live 在持锁状态下解析仍可用的句柄。
// 从未分配的句柄返回 ReasonHandleNotFound；已释放完的返回 ReasonHandleReleased。
func (m *Manager) live(handle int, op string) (*variable, error) {
	v := m.vars[handle]
	if v == nil {
		return nil, newOpError(op, ReasonHandleNotFound)
	}
	if v.holders == 0 {
		return nil, newOpError(op, ReasonHandleReleased)
	}
	return v, nil
}
