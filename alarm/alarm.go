// Package alarm 实现一个按键维护累计值的跨阈值报警流。
//
// 每个键拥有独立的累计值（从 0 开始，可增可减）与报警开关状态。
// 当累计值由关闭态达到或超过阈值时产生一次“报警”边沿事件；
// 当累计值由开启态跌破阈值减去迟滞带时产生一次“解除报警”边沿事件。
// 迟滞带保证阈值附近的抖动不会造成事件重复，且所有判定均可通过
// 按输入顺序逐步重放来复现。
package alarm

import (
	"strconv"
	"sync"
)

// EventType 是报警流事件的类型。
type EventType string

const (
	// EventAlarm 表示累计值自关闭态跨越上沿（达到或超过阈值）。
	EventAlarm EventType = "ALARM"
	// EventClear 表示累计值自开启态跨越下沿（跌破阈值-迟滞带）。
	EventClear EventType = "CLEAR"
	// EventNone 表示本次更新未产生边沿事件。
	EventNone EventType = "NONE"
)

// Event 记录一次报警流更新的判定结果。
// ALARM / CLEAR 为真实边沿事件，会追加到事件列表；NONE 不追加。
type Event struct {
	Seq    int64     // 全局序号，从 1 开始，按事件提交顺序分配
	Key    string    // 事件所属键
	Type   EventType // 事件类型
	Value  int64     // 更新后的累计值
	Delta  int64     // 本次增减量
	Armed  bool      // 更新后的报警开关状态
	Reason string    // 判定依据的可读说明
}

// Stream 是按键维护累计值、带迟滞的跨阈值报警流。
type Stream struct {
	threshold  int64
	hysteresis int64

	mu       sync.RWMutex // 仅保护 keys 映射本身的增删与快照读取
	keys     map[string]*keyState
	eventMu  sync.Mutex // 保护全局事件序号
	eventSeq int64
}

type keyState struct {
	mu     sync.Mutex
	value  int64
	armed  bool
	events []Event
}

// New 创建报警流。threshold 为报警阈值，hysteresis 为迟滞带宽度，
// 解除下沿为 threshold-hysteresis。
func New(threshold, hysteresis int64) (*Stream, error) {
	if threshold <= 0 {
		return nil, ErrNonPositiveThreshold
	}
	if hysteresis <= 0 {
		return nil, ErrNonPositiveHysteresis
	}
	if hysteresis >= threshold {
		return nil, ErrHysteresisTooLarge
	}
	return &Stream{
		threshold:  threshold,
		hysteresis: hysteresis,
		keys:       make(map[string]*keyState),
	}, nil
}

// Add 将 delta 累加到 key 的累计值上，并返回本次更新的判定结果。
// 判定失败（空键或溢出）时整体拒绝：任何键的值、状态与事件列表均不变。
func (s *Stream) Add(key string, delta int64) (Event, error) {
	if key == "" {
		return Event{}, ErrEmptyKey
	}
	ks := s.getKeyForUpdate(key)

	ks.mu.Lock()
	defer ks.mu.Unlock()

	newValue, ok := addInt64(ks.value, delta)
	if !ok {
		return Event{}, ErrOverflow
	}

	ev := Event{
		Key:   key,
		Value: newValue,
		Delta: delta,
	}

	switch {
	case !ks.armed && newValue >= s.threshold:
		ev.Type = EventAlarm
		ev.Armed = true
		ev.Reason = "closed -> new value " + itoa(newValue) +
			" >= threshold " + itoa(s.threshold) + ": raise alarm"
		ks.armed = true
	case ks.armed && newValue < s.threshold-s.hysteresis:
		ev.Type = EventClear
		ev.Armed = false
		ev.Reason = "armed -> new value " + itoa(newValue) +
			" < lower bound " + itoa(s.threshold-s.hysteresis) +
			" (threshold " + itoa(s.threshold) + " - hysteresis " + itoa(s.hysteresis) +
			"): clear alarm"
		ks.armed = false
	default:
		ev.Type = EventNone
		ev.Armed = ks.armed
		if ks.armed {
			ev.Reason = "armed and new value " + itoa(newValue) +
				" >= lower bound " + itoa(s.threshold-s.hysteresis) + ": stay armed"
		} else {
			ev.Reason = "closed and new value " + itoa(newValue) +
				" < threshold " + itoa(s.threshold) + ": stay closed"
		}
	}

	ks.value = newValue
	if ev.Type != EventNone {
		s.eventMu.Lock()
		s.eventSeq++
		ev.Seq = s.eventSeq
		s.eventMu.Unlock()
		ks.events = append(ks.events, ev)
	}
	return ev, nil
}

// Value 返回 key 当前的累计值（不存在的键返回 0）。
func (s *Stream) Value(key string) int64 {
	ks := s.getKey(key)
	if ks == nil {
		return 0
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.value
}

// Armed 返回 key 当前的报警开关状态（不存在的键返回 false）。
func (s *Stream) Armed(key string) bool {
	ks := s.getKey(key)
	if ks == nil {
		return false
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.armed
}

// Events 返回 key 的边沿事件列表快照（按发生顺序）。
func (s *Stream) Events(key string) []Event {
	ks := s.getKey(key)
	if ks == nil {
		return nil
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	out := make([]Event, len(ks.events))
	copy(out, ks.events)
	return out
}

// Keys 返回当前已知键的快照（顺序不保证，调用方不应依赖）。
func (s *Stream) Keys() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.keys))
	for k := range s.keys {
		out = append(out, k)
	}
	return out
}

// Threshold 返回报警阈值。
func (s *Stream) Threshold() int64 { return s.threshold }

// LowerBound 返回解除报警下沿（threshold-hysteresis）。
func (s *Stream) LowerBound() int64 { return s.threshold - s.hysteresis }

func (s *Stream) getKey(key string) *keyState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.keys[key]
}

func (s *Stream) getKeyForUpdate(key string) *keyState {
	s.mu.RLock()
	ks, ok := s.keys[key]
	s.mu.RUnlock()
	if ok {
		return ks
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ks, ok := s.keys[key]; ok {
		return ks
	}
	ks = &keyState{}
	s.keys[key] = ks
	return ks
}

// addInt64 计算 a+b，结果溢出 int64 时返回 ok=false。
func addInt64(a, b int64) (int64, bool) {
	if b > 0 && a > maxInt64-b {
		return 0, false
	}
	if b < 0 && a < minInt64-b {
		return 0, false
	}
	return a + b, true
}

const (
	maxInt64 = 1<<63 - 1
	minInt64 = -1 << 63
)

func itoa(v int64) string { return strconv.FormatInt(v, 10) }
