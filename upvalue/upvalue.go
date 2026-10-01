// Package upvalue 实现闭包捕获变量（upvalue）的开闭管理器。
//
// 捕获变量在开放状态下共享值栈槽位，读写直接作用于栈；
// 作用域退出时按层关闭，把槽值复制进自身存储，之后与栈互不影响。
package upvalue

import "sync"

// Value 是栈槽与捕获变量中存储的值。
type Value = int64

// Handle 是捕获变量的句柄，从 1 起递增且不复用。
type Handle int

// Manager 管理值栈与捕获变量的开闭状态，所有方法可并发调用，
// 结果等价于某个串行顺序。
type Manager struct {
	mu      sync.Mutex
	stack   []Value
	open    map[int]*upvalue // 共享表：槽号 -> 开放捕获变量
	handles map[Handle]*upvalue
	nextID  Handle
}

// upvalue 是一个捕获变量，开放时指向栈槽，关闭后持有自身存储。
type upvalue struct {
	id       Handle
	slot     int
	closed   bool
	stored   Value
	refcount int
}

// New 创建一个空的管理器。
func New() *Manager {
	return &Manager{
		open:    make(map[int]*upvalue),
		handles: make(map[Handle]*upvalue),
		nextID:  1,
	}
}

// Top 返回当前栈顶（即栈中槽的数量）。
func (m *Manager) Top() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.stack)
}

// Push 把值压入栈，返回其槽号，栈顶加一。
func (m *Manager) Push(v Value) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stack = append(m.stack, v)
	return len(m.stack) - 1
}

// Capture 捕获槽 slot，返回捕获变量句柄。
// 同一槽上已有开放的捕获变量时返回同一句柄，否则新建句柄；
// 每次捕获使该变量持有数加一。槽号不小于栈顶时整体拒绝。
func (m *Manager) Capture(slot int) (Handle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return 0, ErrSlotOutOfRange
	}
	if uv, ok := m.open[slot]; ok {
		uv.refcount++
		return uv.id, nil
	}
	uv := &upvalue{id: m.nextID, slot: slot, refcount: 1}
	m.nextID++
	m.open[slot] = uv
	m.handles[uv.id] = uv
	return uv.id, nil
}

// ReadStack 读取栈槽的值，越界时拒绝。
func (m *Manager) ReadStack(slot int) (Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return 0, ErrStackOutOfRange
	}
	return m.stack[slot], nil
}

// WriteStack 写入栈槽，越界时拒绝。
func (m *Manager) WriteStack(slot int, v Value) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if slot < 0 || slot >= len(m.stack) {
		return ErrStackOutOfRange
	}
	m.stack[slot] = v
	return nil
}

// Close 从层 level 关闭：对槽号不小于 level 的全部开放变量，
// 把当前槽值复制进自身存储并转为关闭，随后栈顶设为 level。
// level 恰等于栈顶是合法的空操作；level 小于 0 或大于栈顶时整体拒绝。
func (m *Manager) Close(level int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if level < 0 || level > len(m.stack) {
		return ErrInvalidLevel
	}
	for slot, uv := range m.open {
		if slot >= level {
			uv.stored = m.stack[slot]
			uv.closed = true
			delete(m.open, slot)
		}
	}
	m.stack = m.stack[:level]
	return nil
}

// lookup 校验句柄存在且未释放完。
func (m *Manager) lookup(h Handle) (*upvalue, error) {
	uv, ok := m.handles[h]
	if !ok {
		return nil, ErrHandleNotFound
	}
	if uv.refcount == 0 {
		return nil, ErrHandleReleased
	}
	return uv, nil
}

// Read 读取捕获变量：开放时读栈槽，关闭后读自身存储。
func (m *Manager) Read(h Handle) (Value, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	uv, err := m.lookup(h)
	if err != nil {
		return 0, err
	}
	if uv.closed {
		return uv.stored, nil
	}
	return m.stack[uv.slot], nil
}

// Write 写入捕获变量：开放时写栈槽，关闭后写自身存储。
func (m *Manager) Write(h Handle, v Value) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	uv, err := m.lookup(h)
	if err != nil {
		return err
	}
	if uv.closed {
		uv.stored = v
		return nil
	}
	m.stack[uv.slot] = v
	return nil
}

// Release 释放句柄使持有数减一；减到 0 时若仍开放则从共享表摘除，
// 之后同槽再捕获得到新句柄。句柄不存在或已释放完时拒绝。
func (m *Manager) Release(h Handle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	uv, err := m.lookup(h)
	if err != nil {
		return err
	}
	uv.refcount--
	if uv.refcount == 0 && !uv.closed {
		delete(m.open, uv.slot)
	}
	return nil
}
