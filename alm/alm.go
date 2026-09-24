// Package alm 按 Key 维护累计值与报警状态，产出事件流。
package alm

import (
	"errors"
	"fmt"
	"sync"

	"ontology/thr"
)

// 可判定哨兵错误，互不相同。
var (
	ErrBadParam      = errors.New("alm: T<=0 or H<=0")
	ErrBadHysteresis = errors.New("alm: H>=T, debounce edge T-H<=0")
	ErrEmptyKey      = errors.New("alm: empty key")
	ErrOverflow      = errors.New("alm: value overflows int64")
	ErrTooManyKeys   = errors.New("alm: key count exceeds maxKeys")
)

// Event 是一次状态翻转产生的事件。
type Event struct {
	Seq   int64    // 全局发生顺序，从 1 开始
	Key   string   //
	Kind  thr.Kind // KindOn 或 KindOff
	Value int64    // 事件发生时该 Key 的累计值
}

type keyState struct {
	value int64
	on    bool
}

// Store 是报警流的核心状态，并发安全。
type Store struct {
	mu      sync.Mutex
	t, h    int64
	maxKeys int
	keys    map[string]*keyState
	events  []Event
	// lastChecked 记录最近一次 Add 为判定跨阈值检查过的 Key 个数。
	// 非导出，不出现在任何公开接口；仅同包白盒测试可读。
	lastChecked int
}

// NewStore 校验配置并返回空 Store。
func NewStore(t, h int64, maxKeys int) (*Store, error) {
	if t <= 0 || h <= 0 {
		return nil, ErrBadParam
	}
	if h >= t {
		return nil, ErrBadHysteresis
	}
	if maxKeys <= 0 {
		return nil, fmt.Errorf("%w: maxKeys=%d", ErrBadParam, maxKeys)
	}
	return &Store{t: t, h: h, maxKeys: maxKeys, keys: make(map[string]*keyState)}, nil
}

// Add 对 key 累加 delta，返回本次产生的事件（0 或 1 条）。
// 先完成全部校验再改任何状态：被拒操作不留痕。
func (s *Store) Add(key string, delta int64) ([]Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastChecked = 0
	if key == "" {
		return nil, ErrEmptyKey
	}
	st, ok := s.keys[key]
	if !ok {
		if len(s.keys) >= s.maxKeys {
			return nil, ErrTooManyKeys
		}
		st = &keyState{}
	}
	nv := st.value + delta
	if (delta > 0 && nv < st.value) || (delta < 0 && nv > st.value) {
		return nil, ErrOverflow
	}
	// 只检查被更新的这一个 Key。
	s.lastChecked = 1
	on, kind := thr.Judge(st.value, nv, st.on, s.t, s.h)
	st.value = nv
	st.on = on
	if !ok {
		s.keys[key] = st
	}
	if kind == thr.KindNone {
		return nil, nil
	}
	ev := Event{Seq: int64(len(s.events)) + 1, Key: key, Kind: kind, Value: nv}
	s.events = append(s.events, ev)
	return []Event{ev}, nil
}

// View 返回各 Key 当前累计值的快照。
func (s *Store) View() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.keys))
	for k, st := range s.keys {
		out[k] = st.value
	}
	return out
}

// Alarm 返回各 Key 当前 on 状态的快照。
func (s *Store) Alarm() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.keys))
	for k, st := range s.keys {
		out[k] = st.on
	}
	return out
}

// Events 返回全部事件的快照，按发生顺序。
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Event, len(s.events))
	copy(out, s.events)
	return out
}

// VerifyAddCost 内部自建 m 个互不相关 Key 的 Store，再对一个 Key 做一次 Add，
// 核验检查个数不超过与 m 无关的小常数。只返回结论，不暴露计数器数值。
func VerifyAddCost() bool {
	for _, m := range []int{100, 1000, 10000} {
		s, err := NewStore(1<<40, 1, m+1)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			if _, err := s.Add(fmt.Sprintf("k%d", i), 1); err != nil {
				return false
			}
		}
		if _, err := s.Add("k0", 1); err != nil {
			return false
		}
		if s.lastChecked > 1 {
			return false
		}
	}
	return true
}
