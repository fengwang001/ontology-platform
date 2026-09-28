package cep

import (
	"log/slog"
	"sync"
)

// keyState 是单个键的内部状态。
type keyState struct {
	// last 为该键上一个事件，用于严格连续判定与时间单调性校验。
	last *Event
	// pending 为按到达顺序排列、尚未配对且未过期的先事件。
	pending []Event
}

func (s *keyState) clone() *keyState {
	cp := &keyState{}
	if s.last != nil {
		e := *s.last
		cp.last = &e
	}
	if s.pending != nil {
		cp.pending = append([]Event(nil), s.pending...)
	}
	return cp
}

// Matcher 在同一键的事件流中寻找满足时间窗口的先后事件配对。
type Matcher struct {
	cfg Config
	log *slog.Logger

	mu     sync.RWMutex
	states map[string]*keyState
	pairs  []Pair
}

// NewMatcher 校验配置并创建匹配器。
// 拒绝原因固定为 ErrInvalidConfig（可被 errors.Is 识别）。
func NewMatcher(cfg Config, log *slog.Logger) (*Matcher, error) {
	if cfg.FirstType == "" || cfg.SecondType == "" ||
		cfg.FirstType == cfg.SecondType || cfg.Window < 0 ||
		!cfg.Mode.valid() || cfg.MaxPending < 0 {
		return nil, &RejectError{Err: ErrInvalidConfig}
	}
	if log == nil {
		log = slog.Default()
	}
	return &Matcher{
		cfg:    cfg,
		log:    log,
		states: make(map[string]*keyState),
	}, nil
}

// Process 原子地处理一个批次：要么全部生效并返回本批次新产生的配对
// （按批次内后事件出现顺序排列），要么整体被拒绝，且队列、上一个事件、
// 已输出配对均保持不变。
func (m *Matcher) Process(events []Event) ([]Pair, error) {
	m.log.LogAttrs(nil, slog.LevelInfo, "cep: batch received",
		slog.Int("size", len(events)),
		slog.String("mode", modeName(m.cfg.Mode)),
		slog.Int64("window", m.cfg.Window),
	)
	for i, e := range events {
		m.log.LogAttrs(nil, slog.LevelDebug, "cep: event input",
			slog.Int("index", i),
			slog.String("key", e.Key),
			slog.String("type", e.Type),
			slog.Int64("time", e.Time),
			slog.Int64("sequence", e.Sequence),
		)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 先在状态克隆上模拟整批，任何一步失败都直接丢弃克隆。
	simStates := make(map[string]*keyState, len(m.states))
	for k, s := range m.states {
		simStates[k] = s.clone()
	}
	simPairs := make([]Pair, 0, len(events)/2+1)

	for i, e := range events {
		if e.Key == "" {
			m.reject(i, e, ErrEmptyKey, "key or type is empty")
			return nil, &RejectError{Err: ErrEmptyKey, Index: i}
		}
		if e.Type == "" {
			m.reject(i, e, ErrEmptyType, "key or type is empty")
			return nil, &RejectError{Err: ErrEmptyType, Index: i}
		}

		st := simStates[e.Key]
		if st == nil {
			st = &keyState{}
			simStates[e.Key] = st
		}

		// 同键时间必须单调不减。
		if st.last != nil && e.Time < st.last.Time {
			m.reject(i, e, ErrTimeRegression,
				"time goes backwards vs previous event")
			return nil, &RejectError{Err: ErrTimeRegression, Index: i}
		}

		// 时间只会前进：先淘汰窗口外的待匹配先事件（闭区间下界）。
		drop := 0
		for drop < len(st.pending) && e.Time-st.pending[drop].Time > m.cfg.Window {
			drop++
		}
		if drop > 0 {
			st.pending = append([]Event(nil), st.pending[drop:]...)
		}

		switch {
		case e.Type == m.cfg.FirstType:
			if m.cfg.Mode == Strict && st.last != nil && st.last.Type != m.cfg.SecondType {
				// 严格连续：只有“上一个事件是已与它配对的后事件”时
				// 才允许保留更早的待匹配先事件；其余情况链已被打断。
				st.pending = nil
			}
			if m.cfg.MaxPending > 0 && len(st.pending) >= m.cfg.MaxPending {
				m.reject(i, e, ErrQueueOverflow, "pending queue limit reached")
				return nil, &RejectError{Err: ErrQueueOverflow, Index: i}
			}
			st.pending = append(st.pending, e)
			m.log.LogAttrs(nil, slog.LevelDebug, "cep: event queued as first",
				slog.Int("index", i), slog.String("key", e.Key),
				slog.Int("pending", len(st.pending)))

		case e.Type == m.cfg.SecondType:
			matched := false
			if m.cfg.Mode == Strict {
				// 后事件必须紧挨着先事件：只与上一个事件比较。
				if st.last != nil && st.last.Type == m.cfg.FirstType && len(st.pending) > 0 &&
					st.pending[len(st.pending)-1].Sequence == st.last.Sequence &&
					st.pending[len(st.pending)-1].Time == st.last.Time {
					first := st.pending[len(st.pending)-1]
					st.pending = st.pending[:len(st.pending)-1]
					simPairs = append(simPairs, Pair{First: first, Second: e})
					matched = true
					m.matched(i, first, e, "strict adjacency satisfied")
				}
			} else if len(st.pending) > 0 {
				// 宽松连续：取最早的待匹配先事件（FIFO），
				// 保证一个先事件至多配一个后事件。
				first := st.pending[0]
				st.pending = st.pending[1:]
				simPairs = append(simPairs, Pair{First: first, Second: e})
				matched = true
				m.matched(i, first, e, "relaxed contiguity, within window")
			}
			if !matched {
				// 未配对的后事件打断严格连续链；宽松模式下它也只是普通中间事件。
				if m.cfg.Mode == Strict {
					st.pending = nil
				}
				m.log.LogAttrs(nil, slog.LevelDebug, "cep: no match for second event",
					slog.Int("index", i), slog.String("key", e.Key),
					slog.String("reason", noMatchReason(m.cfg.Mode, st)))
			}

		default:
			// 其他类型事件：严格连续下打断相邻链，宽松连续下允许夹在中间。
			if m.cfg.Mode == Strict {
				st.pending = nil
			}
			m.log.LogAttrs(nil, slog.LevelDebug, "cep: intermediate event",
				slog.Int("index", i), slog.String("key", e.Key),
				slog.String("type", e.Type),
				slog.String("contiguity", modeName(m.cfg.Mode)))
		}

		cur := e
		st.last = &cur
	}

	// 模拟全部成功，提交。
	m.states = simStates
	m.pairs = append(m.pairs, simPairs...)
	m.log.LogAttrs(nil, slog.LevelInfo, "cep: batch committed",
		slog.Int("new_pairs", len(simPairs)),
		slog.Int("total_pairs", len(m.pairs)))

	out := append([]Pair(nil), simPairs...)
	return out, nil
}

// Pairs 返回截至目前所有已输出配对的完整快照，可被并发读取。
func (m *Matcher) Pairs() []Pair {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Pair(nil), m.pairs...)
}

// Pending 返回某键当前待匹配先事件数的快照。
func (m *Matcher) Pending(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s := m.states[key]; s != nil {
		return len(s.pending)
	}
	return 0
}

func (m *Matcher) matched(index int, first, second Event, reason string) {
	m.log.LogAttrs(nil, slog.LevelInfo, "cep: pair matched",
		slog.Int("index", index),
		slog.String("key", second.Key),
		slog.Int64("first_time", first.Time),
		slog.Int64("second_time", second.Time),
		slog.Int64("diff", second.Time-first.Time),
		slog.Int64("window", m.cfg.Window),
		slog.String("reason", reason),
	)
}

func (m *Matcher) reject(index int, e Event, reason error, detail string) {
	m.log.LogAttrs(nil, slog.LevelWarn, "cep: batch rejected, state unchanged",
		slog.Int("index", index),
		slog.String("key", e.Key),
		slog.String("type", e.Type),
		slog.Int64("time", e.Time),
		slog.String("reason", reason.Error()),
		slog.String("detail", detail),
	)
}

func modeName(m Mode) string {
	if m == Strict {
		return "strict"
	}
	return "relaxed"
}

func noMatchReason(m Mode, st *keyState) string {
	if m == Strict {
		if st.last == nil {
			return "no previous event"
		}
		return "previous event is not the first type or chain broken"
	}
	return "no pending first event within window"
}
