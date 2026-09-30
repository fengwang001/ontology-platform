// Package window 提供带清理与复活语义的窗口状态管理器。
//
// 状态迁移：
//
//	(不存在) --首个事件--> Active
//	Active   --清理------> Cleaned
//	Cleaned  --迟到事件--> Revived   （复活：累计值取该事件值，不触发）
//	Cleaned  --回收------> (删除)     （回收与复活互斥，由互斥锁串行化）
//	Revived  --任何事件--> Revived   （照常累加，但不再触发）
//
// 触发规则：仅 Active 窗口在累计值从下往上越过阈值的整数倍时触发，
// 每越过一个倍数触发次数加一并产出一条触发事件。
package window

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// 四类非法输入对应的可判定错误，互不相同。
var (
	// ErrInvalidID 窗口标识非法（空或仅空白字符）。
	ErrInvalidID = errors.New("window: invalid window id")
	// ErrInvalidValue 事件值非法（非正数）。
	ErrInvalidValue = errors.New("window: invalid event value")
	// ErrWindowNotFound 对从未创建过（或已被回收）的窗口发送迟到事件。
	ErrWindowNotFound = errors.New("window: window not found")
	// ErrTooManyWindows 窗口数超过上限，拒绝创建新窗口。
	ErrTooManyWindows = errors.New("window: too many windows")
	// ErrWindowCleaned 窗口已清理，普通事件不再参与累计（需用迟到事件复活）。
	ErrWindowCleaned = errors.New("window: window is cleaned")
)

// State 窗口状态。
type State int

const (
	// StateActive 活跃：累加并按阈值触发。
	StateActive State = iota
	// StateCleaned 已清理：累计值冻结保留，不再参与；可被迟到事件复活或被回收。
	StateCleaned
	// StateRevived 已复活：照常累加但不再触发；回收时保留。
	StateRevived
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "Active"
	case StateCleaned:
		return "Cleaned"
	case StateRevived:
		return "Revived"
	default:
		return "Unknown"
	}
}

// TriggerEvent 一次阈值触发产出的事件。
type TriggerEvent struct {
	WindowID string // 窗口标识
	Total    int64  // 触发后的累计值
	Count    int64  // 该窗口累计触发次数
}

// Snapshot 某一窗口状态的一致性快照。
type Snapshot struct {
	ID       string
	Total    int64
	Triggers int64
	State    State
}

type windowState struct {
	total    int64
	triggers int64
	state    State
}

// Manager 窗口状态管理器，所有方法可并发调用。
type Manager struct {
	mu         sync.RWMutex
	threshold  int64
	maxWindows int
	windows    map[string]*windowState
}

// NewManager 创建管理器。threshold 为触发阈值（须为正），maxWindows 为窗口数上限（须为正）。
func NewManager(threshold int64, maxWindows int) (*Manager, error) {
	if threshold <= 0 {
		return nil, ErrInvalidValue
	}
	if maxWindows <= 0 {
		return nil, ErrTooManyWindows
	}
	return &Manager{
		threshold:  threshold,
		maxWindows: maxWindows,
		windows:    make(map[string]*windowState),
	}, nil
}

// AddEvent 处理普通事件：首次到达隐式创建窗口；活跃窗口累加并按阈值触发。
// 返回本次产出的触发事件（可能多条）。
func (m *Manager) AddEvent(id string, value int64) ([]TriggerEvent, error) {
	if err := validate(id, value); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.windows[id]
	if !ok {
		if len(m.windows) >= m.maxWindows {
			return nil, ErrTooManyWindows
		}
		w = &windowState{state: StateActive}
		m.windows[id] = w
	}
	if w.state == StateCleaned {
		return nil, ErrWindowCleaned
	}
	return m.accumulate(id, w, value), nil
}

// LateEvent 处理迟到事件：窗口已清理则复活（累计值取本条事件值，不触发）；
// 窗口不存在则拒绝；其余状态按普通累加规则处理（活跃可触发，已复活不触发）。
func (m *Manager) LateEvent(id string, value int64) ([]TriggerEvent, error) {
	if err := validate(id, value); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.windows[id]
	if !ok {
		return nil, ErrWindowNotFound
	}
	if w.state == StateCleaned {
		// 复活：丢弃冻结历史，累计值只取这一条事件的值，且不触发。
		w.total = value
		w.state = StateRevived
		return nil, nil
	}
	return m.accumulate(id, w, value), nil
}

// Cleanup 清理窗口：进入已清理状态，累计值冻结保留但不再参与。
func (m *Manager) Cleanup(id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidID
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	w, ok := m.windows[id]
	if !ok {
		return ErrWindowNotFound
	}
	if w.state == StateActive {
		w.state = StateCleaned
	}
	return nil
}

// Reap 回收：删除所有已清理且未复活的窗口，返回被回收的窗口标识。
// 与复活互斥：同一窗口要么被回收、要么被复活。
func (m *Manager) Reap() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var reaped []string
	for id, w := range m.windows {
		if w.state == StateCleaned {
			delete(m.windows, id)
			reaped = append(reaped, id)
		}
	}
	sort.Strings(reaped)
	return reaped
}

// Snapshot 返回指定窗口的快照；窗口不存在时 ok 为 false。
func (m *Manager) Snapshot(id string) (Snapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.windows[id]
	if !ok {
		return Snapshot{}, false
	}
	return Snapshot{ID: id, Total: w.total, Triggers: w.triggers, State: w.state}, true
}

// Snapshots 返回全部窗口的快照。
func (m *Manager) Snapshots() []Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snaps := make([]Snapshot, 0, len(m.windows))
	for id, w := range m.windows {
		snaps = append(snaps, Snapshot{ID: id, Total: w.total, Triggers: w.triggers, State: w.state})
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].ID < snaps[j].ID })
	return snaps
}

// TotalTriggers 返回所有窗口的累计触发次数之和。
func (m *Manager) TotalTriggers() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var total int64
	for _, w := range m.windows {
		total += w.triggers
	}
	return total
}

// Len 返回当前窗口数。
func (m *Manager) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.windows)
}

// accumulate 累加事件值；仅活跃窗口按阈值触发，已复活窗口只累加不触发。
// 调用方须持有写锁。
func (m *Manager) accumulate(id string, w *windowState, value int64) []TriggerEvent {
	if w.state != StateActive {
		w.total += value
		return nil
	}
	before := w.total / m.threshold
	w.total += value
	after := w.total / m.threshold
	crossed := after - before
	if crossed <= 0 {
		return nil
	}
	events := make([]TriggerEvent, 0, crossed)
	for i := int64(0); i < crossed; i++ {
		w.triggers++
		events = append(events, TriggerEvent{WindowID: id, Total: w.total, Count: w.triggers})
	}
	return events
}

func validate(id string, value int64) error {
	if strings.TrimSpace(id) == "" {
		return ErrInvalidID
	}
	if value <= 0 {
		return ErrInvalidValue
	}
	return nil
}
