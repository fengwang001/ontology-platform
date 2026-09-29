// Package alert 提供跨阈值报警流：按键维护累计值，
// 在跨越阈值时发出边沿触发的报警与解除事件，并带迟滞带。
package alert

import (
	"errors"
	"fmt"
	"sync"
)

// 可区分的非法参数原因，均可用 errors.Is 判定。
var (
	ErrThresholdNonPositive   = errors.New("alert: threshold must be positive")
	ErrHysteresisNonPositive  = errors.New("alert: hysteresis must be positive")
	ErrHysteresisNotBelowThld = errors.New("alert: hysteresis must be smaller than threshold")
	ErrEmptyKey               = errors.New("alert: key must not be empty")
	ErrOverflow               = errors.New("alert: accumulated value overflow")
)

// State 表示某个键的报警状态。
type State int

const (
	// StateClosed 关闭态：未报警。
	StateClosed State = iota
	// StateOpen 开启态：报警中。
	StateOpen
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	default:
		return "unknown"
	}
}

// EventType 表示事件类型。
type EventType int

const (
	// EventAlarm 报警事件：关闭态下累计值达到或超过阈值时触发。
	EventAlarm EventType = iota
	// EventClear 解除事件：开启态下累计值跌破（阈值-迟滞带）时触发。
	EventClear
)

func (t EventType) String() string {
	switch t {
	case EventAlarm:
		return "alarm"
	case EventClear:
		return "clear"
	default:
		return "unknown"
	}
}

// Config 为报警流配置，构造时整体校验。
type Config struct {
	Threshold  int64 // 触发阈值，必须为正
	Hysteresis int64 // 迟滞带，必须为正且小于阈值
}

func (c Config) validate() error {
	if c.Threshold <= 0 {
		return fmt.Errorf("%w, got %d", ErrThresholdNonPositive, c.Threshold)
	}
	if c.Hysteresis <= 0 {
		return fmt.Errorf("%w, got %d", ErrHysteresisNonPositive, c.Hysteresis)
	}
	if c.Hysteresis >= c.Threshold {
		return fmt.Errorf("%w, got hysteresis=%d threshold=%d", ErrHysteresisNotBelowThld, c.Hysteresis, c.Threshold)
	}
	return nil
}

// Event 为一次边沿触发产生的事件。
type Event struct {
	Seq       uint64    // 全局单调递增序号，从 1 开始
	Key       string    // 触发事件的键
	Type      EventType // 事件类型
	Value     int64     // 触发后的累计值
	Threshold int64     // 触发时使用的阈值
	ClearLine int64     // 触发时使用的解除线（阈值-迟滞带）
	Reason    string    // 判定依据，便于核对
}

// Result 为一次增减操作的结果。
type Result struct {
	Key   string // 操作的键
	Value int64  // 操作后的累计值
	State State  // 操作后的报警状态
	Event *Event // 本次触发的事件；未触发时为 nil
}

type keyState struct {
	value int64
	state State
}

// Monitor 为跨阈值报警流。所有方法均可并发调用；
// 内部以互斥锁串行化，保证并发结果与某一串行参照一致。
type Monitor struct {
	mu     sync.RWMutex
	cfg    Config
	keys   map[string]*keyState
	events []Event
	seq    uint64
}

// NewMonitor 构造报警流；配置非法时整体拒绝并返回可区分的原因。
func NewMonitor(cfg Config) (*Monitor, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Monitor{cfg: cfg, keys: make(map[string]*keyState)}, nil
}

// ClearLine 返回解除线：阈值-迟滞带。
// 开启态下累计值严格小于解除线才解除；恰好等于解除线仍保持开启。
func (m *Monitor) ClearLine() int64 {
	return m.cfg.Threshold - m.cfg.Hysteresis
}

// Add 对 key 的累计值增加 delta（delta 可为负表示减少）。
// 键为空或累计值溢出时整体拒绝：不改变任何键的值、状态与事件列表。
func (m *Monitor) Add(key string, delta int64) (Result, error) {
	if key == "" {
		return Result{}, ErrEmptyKey
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	ks := m.keys[key]
	if ks == nil {
		ks = &keyState{}
	}
	newValue := ks.value + delta
	if (delta > 0 && newValue < ks.value) || (delta < 0 && newValue > ks.value) {
		return Result{}, fmt.Errorf("%w: key=%q value=%d delta=%d", ErrOverflow, key, ks.value, delta)
	}

	res := Result{Key: key, Value: newValue, State: ks.state}
	var ev *Event
	switch ks.state {
	case StateClosed:
		if newValue >= m.cfg.Threshold {
			ks.state = StateOpen
			res.State = StateOpen
			ev = m.appendEvent(key, EventAlarm, newValue,
				fmt.Sprintf("state=closed && value(%d) >= threshold(%d) => alarm, state->open", newValue, m.cfg.Threshold))
		}
	case StateOpen:
		if newValue < m.ClearLine() {
			ks.state = StateClosed
			res.State = StateClosed
			ev = m.appendEvent(key, EventClear, newValue,
				fmt.Sprintf("state=open && value(%d) < clearLine(%d) => clear, state->closed", newValue, m.ClearLine()))
		}
	}
	ks.value = newValue
	m.keys[key] = ks
	res.Event = ev
	return res, nil
}

func (m *Monitor) appendEvent(key string, typ EventType, value int64, reason string) *Event {
	m.seq++
	ev := Event{
		Seq:       m.seq,
		Key:       key,
		Type:      typ,
		Value:     value,
		Threshold: m.cfg.Threshold,
		ClearLine: m.ClearLine(),
		Reason:    reason,
	}
	m.events = append(m.events, ev)
	return &m.events[len(m.events)-1]
}

// Value 返回 key 的当前累计值；不存在的键返回 0。
func (m *Monitor) Value(key string) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ks := m.keys[key]; ks != nil {
		return ks.value
	}
	return 0
}

// State 返回 key 的当前报警状态；不存在的键为关闭态。
func (m *Monitor) State(key string) State {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if ks := m.keys[key]; ks != nil {
		return ks.state
	}
	return StateClosed
}

// Events 返回全局事件列表的副本，按 Seq 升序。
func (m *Monitor) Events() []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// EventsFor 返回指定键的事件列表，按 Seq 升序。
func (m *Monitor) EventsFor(key string) []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Event
	for _, ev := range m.events {
		if ev.Key == key {
			out = append(out, ev)
		}
	}
	return out
}

// Step 为重放用的一步操作。
type Step struct {
	Key   string
	Delta int64
}

// Replay 在全新报警流上逐步重放 steps，返回完整事件序列。
// 任一步非法即整体失败并返回该步错误；用于本地核对事件序列的可复现性。
func Replay(cfg Config, steps []Step) ([]Event, error) {
	m, err := NewMonitor(cfg)
	if err != nil {
		return nil, err
	}
	for i, s := range steps {
		if _, err := m.Add(s.Key, s.Delta); err != nil {
			return nil, fmt.Errorf("replay step %d (%+v): %w", i, s, err)
		}
	}
	return m.Events(), nil
}
