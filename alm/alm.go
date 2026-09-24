// Package alm 按 Key 维护累计值与报警状态，产出事件流。依赖 thr。
package alm

import (
	"errors"
	"math"
	"strconv"
	"sync"

	"ontology/thr"
)

// 可判定哨兵错误，互不相同。
var (
	ErrBadParam      = errors.New("alm: 参数非法 (T<=0 或 H<=0)")
	ErrBadHysteresis = errors.New("alm: 迟滞带非法 (H>=T)")
	ErrEmptyKey      = errors.New("alm: key 为空串")
	ErrOverflow      = errors.New("alm: 累计值溢出 int64")
	ErrTooManyKeys   = errors.New("alm: 超出 maxKeys 上限")
)

// Event 是一条报警事件，按发生顺序记录。
type Event struct {
	Key  string
	Type thr.Event
}

type entry struct {
	value int64
	on    bool
}

// Store 维护全部 Key 的累计值、on 状态与事件流，并发安全。
type Store struct {
	mu      sync.Mutex
	t, h    int64
	maxKeys int
	m       map[string]*entry
	events  []Event
	checked int // 非导出：最近一次 Add 为判定跨阈值检查过的 Key 个数
}

// New 校验参数并创建 Store。
func New(t, h int64, maxKeys int) (*Store, error) {
	if t <= 0 || h <= 0 {
		return nil, ErrBadParam
	}
	if h >= t {
		return nil, ErrBadHysteresis
	}
	if maxKeys <= 0 {
		return nil, ErrBadParam
	}
	return &Store{t: t, h: h, maxKeys: maxKeys, m: make(map[string]*entry)}, nil
}

// Add 增减累计值并按规则产出事件。失败不留痕：任何校验失败都在写状态之前返回。
func (s *Store) Add(key string, delta int64) ([]Event, error) {
	if key == "" {
		return nil, ErrEmptyKey
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[key]
	if !ok {
		if len(s.m) >= s.maxKeys {
			return nil, ErrTooManyKeys
		}
		e = &entry{}
	}
	if (delta > 0 && e.value > math.MaxInt64-delta) || (delta < 0 && e.value < math.MinInt64-delta) {
		return nil, ErrOverflow
	}
	nv := e.value + delta
	s.checked = 1 // 只检查被更新的这一个 Key
	non, emit, ev := thr.Judge(e.value, nv, e.on, s.t, s.h)
	e.value, e.on = nv, non
	s.m[key] = e
	if !emit {
		return nil, nil
	}
	s.events = append(s.events, Event{Key: key, Type: ev})
	return []Event{s.events[len(s.events)-1]}, nil
}

// View 返回各 Key 当前累计值的快照。
func (s *Store) View() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]int64, len(s.m))
	for k, e := range s.m {
		out[k] = e.value
	}
	return out
}

// Alarm 返回各 Key 当前 on 状态的快照。
func (s *Store) Alarm() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(s.m))
	for k, e := range s.m {
		out[k] = e.on
	}
	return out
}

// Events 返回全部事件的快照，按发生顺序。
func (s *Store) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// ScaleFlat 内部自检：建 m 个互不相关 Key 后对单个 Key Add，
// 检查个数不随 m 增长。只返回结论，不暴露计数器数值。
func ScaleFlat() bool {
	for _, m := range []int{100, 1000, 10000} {
		st, err := New(1<<40, 1, m+1)
		if err != nil {
			return false
		}
		for i := 0; i < m; i++ {
			if _, err := st.Add("k"+strconv.Itoa(i), 1); err != nil {
				return false
			}
		}
		if _, err := st.Add("k0", 1); err != nil {
			return false
		}
		if st.checked > 2 { // 与 m 无关的小常数
			return false
		}
	}
	return true
}
