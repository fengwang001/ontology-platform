// Package ontology 提供带清理（cleanup）与复活（revive）语义的窗口状态管理器。
package ontology

import (
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
)

// 窗口生命周期状态。
const (
	// StateActive 活跃：事件正常累加并可能触发。
	StateActive = "active"
	// StateCleaned 已清理：累计值冻结保留，不再参与累加与触发，等待回收或复活。
	StateCleaned = "cleaned"
	// StateRevived 已复活：由迟到事件复活，此后照常累加但永不触发。
	StateRevived = "revived"
)

// 四类互不相同的可判定错误。
var (
	// ErrInvalidID 窗口标识非法（空串或含不可见字符）。
	ErrInvalidID = errors.New("ontology: invalid window id")
	// ErrInvalidValue 事件值非法（负数）。
	ErrInvalidValue = errors.New("ontology: invalid event value")
	// ErrWindowNotFound 窗口从未创建（迟到事件的目标不存在）或已被回收。
	ErrWindowNotFound = errors.New("ontology: window not found")
	// ErrTooManyWindows 窗口数超过构造时给定的上限。
	ErrTooManyWindows = errors.New("ontology: too many windows")
)

// TriggerEvent 是累计值从下往上越过阈值时产出的触发事件。
type TriggerEvent struct {
	WindowID string
	// Seq 该窗口内第几次触发，从 1 开始。
	Seq int
}

// EventResult 描述一次事件（普通事件或迟到事件）的处理结果。
type EventResult struct {
	WindowID string
	// Total 处理后该窗口的累计值。
	Total float64
	// Triggers 本次事件产出的触发事件列表（复活与复活后的事件恒为空）。
	Triggers []TriggerEvent
	// Revived 是否由本次迟到事件完成复活。
	Revived bool
	// State 处理后窗口状态。
	State string
}

// WindowSnapshot 是某个窗口在快照时刻的只读视图。
type WindowSnapshot struct {
	ID        string
	Total     float64
	FireCount int
	State     string
}

// window 记录单个窗口的全部状态。
type window struct {
	mu        sync.Mutex
	id        string
	total     float64
	fireCount int
	state     string
}

// Manager 是并发安全的窗口状态管理器。
type Manager struct {
	mu         sync.RWMutex
	windows    map[string]*window
	threshold  float64
	maxWindows int
	// totalFired 是自管理器创建以来产出的触发事件总数，单调递增，
	// 不受清理、复活（清零窗口历史触发次数）与回收（删除窗口）影响。
	totalFired atomic.Int64
}

// NewManager 创建管理器。threshold 必须为正数，maxWindows 必须大于 0。
func NewManager(threshold float64, maxWindows int) (*Manager, error) {
	if !(threshold > 0) || math.IsNaN(threshold) {
		return nil, errors.New("ontology: threshold must be positive")
	}
	if maxWindows <= 0 {
		return nil, errors.New("ontology: maxWindows must be positive")
	}
	return &Manager{
		windows:    make(map[string]*window),
		threshold:  threshold,
		maxWindows: maxWindows,
	}, nil
}

// Event 向窗口投递一条普通事件，窗口不存在时隐式创建。
func (m *Manager) Event(id string, value float64) (EventResult, error) {
	if !validID(id) {
		return EventResult{}, ErrInvalidID
	}
	if !validValue(value) {
		return EventResult{}, ErrInvalidValue
	}

	m.mu.RLock()
	w, exists := m.windows[id]
	m.mu.RUnlock()
	if !exists {
		// 隐式创建需要 map 写锁；创建前再次确认，避免并发重复创建并在此处统一执行上限校验。
		m.mu.Lock()
		w, exists = m.windows[id]
		if !exists {
			if len(m.windows) >= m.maxWindows {
				m.mu.Unlock()
				return EventResult{}, ErrTooManyWindows
			}
			w = &window{id: id, state: StateActive}
			m.windows[id] = w
		}
		m.mu.Unlock()
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case StateCleaned:
		// 已清理窗口冻结：普通事件不参与累加，等待回收或迟到事件复活。
		return EventResult{WindowID: id, Total: w.total, State: w.state}, nil
	case StateRevived:
		// 复活后照常累加，但永不触发。
		w.total += value
		return EventResult{WindowID: id, Total: w.total, State: w.state}, nil
	default:
		oldTotal := w.total
		newTotal := oldTotal + value
		w.total = newTotal
		// 从下往上越过阈值的次数：连续多次越过则多次触发。
		crossings := crossings(oldTotal, newTotal, m.threshold)
		res := EventResult{WindowID: id, Total: newTotal, State: StateActive}
		for i := 0; i < crossings; i++ {
			w.fireCount++
			m.totalFired.Add(1)
			res.Triggers = append(res.Triggers, TriggerEvent{WindowID: id, Seq: w.fireCount})
		}
		return res, nil
	}
}

// LateEvent 向窗口投递一条迟到事件；目标处于已清理状态时复活该窗口。
func (m *Manager) LateEvent(id string, value float64) (EventResult, error) {
	if !validID(id) {
		return EventResult{}, ErrInvalidID
	}
	if !validValue(value) {
		return EventResult{}, ErrInvalidValue
	}

	// 复活与回收互斥：查找、状态判定与状态生效全部在同一个 Manager 写锁临界区内完成。
	// 已清理未复活的窗口要么被回收（迟到事件随后得到 ErrWindowNotFound），
	// 要么被复活（随后的回收保留它），不可能同时发生。
	m.mu.Lock()
	defer m.mu.Unlock()

	w, exists := m.windows[id]
	if !exists {
		// 从未创建过（或已被回收）的窗口不得由迟到事件复活。
		return EventResult{}, ErrWindowNotFound
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case StateCleaned:
		// 复活只服务这一条事件：丢弃全部冻结历史（累计值与历史触发次数清零），
		// 累计值直接取本条事件的值，不触发。
		w.total = value
		w.fireCount = 0
		w.state = StateRevived
		return EventResult{WindowID: id, Total: value, Revived: true, State: StateRevived}, nil
	case StateRevived:
		// 已复活窗口：迟到事件与普通事件等价，累加但不触发。
		w.total += value
		return EventResult{WindowID: id, Total: w.total, State: StateRevived}, nil
	default:
		// 活跃窗口上迟到事件按普通事件处理（同样可能触发）。
		oldTotal := w.total
		newTotal := oldTotal + value
		w.total = newTotal
		crossings := crossings(oldTotal, newTotal, m.threshold)
		res := EventResult{WindowID: id, Total: newTotal, State: StateActive}
		for i := 0; i < crossings; i++ {
			w.fireCount++
			m.totalFired.Add(1)
			res.Triggers = append(res.Triggers, TriggerEvent{WindowID: id, Seq: w.fireCount})
		}
		return res, nil
	}
}

// Cleanup 将活跃窗口清理为已清理状态，累计值冻结保留。
func (m *Manager) Cleanup(id string) error {
	if !validID(id) {
		return ErrInvalidID
	}

	m.mu.RLock()
	w, exists := m.windows[id]
	m.mu.RUnlock()
	if !exists {
		return ErrWindowNotFound
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.state == StateActive {
		w.state = StateCleaned
	}
	return nil
}

// Reclaim 回收一个已清理且未复活的窗口；已复活的窗口保留。
// 返回 true 表示窗口已被删除，false 表示窗口保留（已复活）。
func (m *Manager) Reclaim(id string) (bool, error) {
	if !validID(id) {
		return false, ErrInvalidID
	}

	// 与复活互斥：判定 + 删除在同一个写锁临界区内完成。
	m.mu.Lock()
	defer m.mu.Unlock()

	w, exists := m.windows[id]
	if !exists {
		return false, ErrWindowNotFound
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	switch w.state {
	case StateCleaned:
		delete(m.windows, id)
		return true, nil
	case StateRevived:
		// 已复活窗口保留，不回收。
		return false, nil
	default:
		// 活跃窗口不可回收。
		return false, nil
	}
}

// Snapshot 返回指定窗口的只读快照。
func (m *Manager) Snapshot(id string) (WindowSnapshot, bool) {
	if !validID(id) {
		return WindowSnapshot{}, false
	}

	m.mu.RLock()
	w, exists := m.windows[id]
	m.mu.RUnlock()
	if !exists {
		return WindowSnapshot{}, false
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return WindowSnapshot{
		ID:        w.id,
		Total:     w.total,
		FireCount: w.fireCount,
		State:     w.state,
	}, true
}

// TotalFireCount 返回当前现存窗口的触发次数之和。
// 复活会清零窗口历史触发次数，回收会删除窗口，二者都会使该和下降。
func (m *Manager) TotalFireCount() int {
	m.mu.RLock()
	ws := make([]*window, 0, len(m.windows))
	for _, w := range m.windows {
		ws = append(ws, w)
	}
	m.mu.RUnlock()

	total := 0
	for _, w := range ws {
		w.mu.Lock()
		total += w.fireCount
		w.mu.Unlock()
	}
	return total
}

// LifetimeFireCount 返回自管理器创建以来产出的触发事件总数（单调递增），
// 不受清理、复活与回收影响，可在任意时刻并发读取。
func (m *Manager) LifetimeFireCount() int {
	return int(m.totalFired.Load())
}

// SnapshotAll 返回当前全部窗口的只读快照。
func (m *Manager) SnapshotAll() []WindowSnapshot {
	m.mu.RLock()
	ids := make([]string, 0, len(m.windows))
	ws := make([]*window, 0, len(m.windows))
	for id, w := range m.windows {
		ids = append(ids, id)
		ws = append(ws, w)
	}
	m.mu.RUnlock()

	snapshots := make([]WindowSnapshot, 0, len(ws))
	for i, w := range ws {
		w.mu.Lock()
		snapshots = append(snapshots, WindowSnapshot{
			ID:        ids[i],
			Total:     w.total,
			FireCount: w.fireCount,
			State:     w.state,
		})
		w.mu.Unlock()
	}
	return snapshots
}

// validID 判定窗口标识是否合法：非空且不能全为空白字符。
func validID(id string) bool {
	return id != "" && strings.TrimSpace(id) != ""
}

// validValue 判定事件值是否合法：非负且非 NaN。
func validValue(value float64) bool {
	return value >= 0 && !math.IsNaN(value)
}

// crossings 计算累计值从 old 增长到 new（new >= old）时从下往上越过 threshold 的次数。
// 采用 floor(new/t) - floor(old/t)，一条事件可产生多次触发，边界恰好落在阈值上算一次越过。
func crossings(old, new, threshold float64) int {
	c := math.Floor(new/threshold) - math.Floor(old/threshold)
	if c < 0 {
		return 0
	}
	return int(c)
}
