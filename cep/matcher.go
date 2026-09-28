package cep

import (
	"fmt"
	"log/slog"
	"sync"
)

// Matcher 在同键事件流上执行“先事件 -> 后事件”的窗口配对。
// 所有方法均可并发调用；结果读取与批处理互斥，保证逐字段一致。
type Matcher struct {
	mu      sync.RWMutex
	cfg     Config
	logger  *slog.Logger
	pending map[string][]Event // 每个键待匹配的先事件队列（按时间升序）
	last    map[string]Event   // 每个键最近一次已接受的事件
	matches []Match            // 已输出的配对（按产生顺序）
}

// New 校验配置并创建匹配器；配置非法时返回 ErrInvalidConfig。
func New(cfg Config, logger *slog.Logger) (*Matcher, error) {
	if cfg.FirstType == "" || cfg.SecondType == "" {
		return nil, fmt.Errorf("%w: first/second type must be non-empty", ErrInvalidConfig)
	}
	if cfg.FirstType == cfg.SecondType {
		return nil, fmt.Errorf("%w: first and second type must differ", ErrInvalidConfig)
	}
	if cfg.MaxWindow <= 0 {
		return nil, fmt.Errorf("%w: max window must be positive, got %d", ErrInvalidConfig, cfg.MaxWindow)
	}
	if cfg.MaxPending <= 0 {
		return nil, fmt.Errorf("%w: max pending must be positive, got %d", ErrInvalidConfig, cfg.MaxPending)
	}
	if cfg.Contiguity != Strict && cfg.Contiguity != Relaxed {
		return nil, fmt.Errorf("%w: unknown contiguity %d", ErrInvalidConfig, cfg.Contiguity)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Matcher{
		cfg:     cfg,
		logger:  logger,
		pending: make(map[string][]Event),
		last:    make(map[string]Event),
	}, nil
}

// ProcessBatch 原子地处理一批事件：
// 全部接受则提交状态并返回本批新产生的配对；
// 任一事件被拒绝则整批拒绝，队列、上一事件与已输出配对均不变。
func (m *Matcher) ProcessBatch(events []Event) ([]Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 在状态副本上模拟处理，全部成功才提交，保证被拒绝的批不产生副作用。
	pending := make(map[string][]Event, len(m.pending))
	for k, q := range m.pending {
		qc := make([]Event, len(q))
		copy(qc, q)
		pending[k] = qc
	}
	last := make(map[string]Event, len(m.last))
	for k, e := range m.last {
		last[k] = e
	}

	var produced []Match
	for i, e := range events {
		match, err := m.processOne(pending, last, e)
		if err != nil {
			m.logger.Info("cep: batch rejected",
				"index", i, "event", e, "reason", err)
			return nil, fmt.Errorf("cep: batch rejected at index %d: %w", i, err)
		}
		if match != nil {
			produced = append(produced, *match)
		}
	}

	m.pending = pending
	m.last = last
	m.matches = append(m.matches, produced...)
	return produced, nil
}

// processOne 在模拟状态上处理单条事件，成功时返回新产生的配对（可能为 nil）。
func (m *Matcher) processOne(pending map[string][]Event, last map[string]Event, e Event) (*Match, error) {
	m.logger.Info("cep: input event", "key", e.Key, "type", e.Type, "timestamp", e.Timestamp)

	if e.Key == "" {
		return nil, fmt.Errorf("%w: type=%q ts=%d", ErrEmptyKey, e.Type, e.Timestamp)
	}
	if e.Type == "" {
		return nil, fmt.Errorf("%w: key=%q ts=%d", ErrEmptyType, e.Key, e.Timestamp)
	}
	if prev, ok := last[e.Key]; ok && e.Timestamp < prev.Timestamp {
		return nil, fmt.Errorf("%w: key=%q ts=%d < last ts=%d",
			ErrTimeRegression, e.Key, e.Timestamp, prev.Timestamp)
	}

	queue := pending[e.Key]

	// 窗口已过期的先事件不可能再配对（同键时间单调不减），直接移出队列。
	kept := queue[:0]
	for _, f := range queue {
		if e.Timestamp-f.Timestamp > m.cfg.MaxWindow {
			m.logger.Info("cep: pending expired",
				"key", e.Key, "first", f, "current_ts", e.Timestamp,
				"reason", "window exceeded")
			continue
		}
		kept = append(kept, f)
	}
	queue = kept

	var match *Match
	switch e.Type {
	case m.cfg.FirstType:
		queue = append(queue, e)
		if len(queue) > m.cfg.MaxPending {
			return nil, fmt.Errorf("%w: key=%q limit=%d", ErrPendingOverflow, e.Key, m.cfg.MaxPending)
		}
	case m.cfg.SecondType:
		match, queue = m.tryMatch(e, queue, last[e.Key])
	}

	pending[e.Key] = queue
	last[e.Key] = e
	return match, nil
}

// tryMatch 按连续性模式为后事件 e 选择配对的先事件。
// 宽松模式：取队列中最早且落在窗口内的先事件（过期者已被剪除，即队首）。
// 严格模式：仅当同键上一条事件是待匹配先事件时才可配对。
// 一个先事件至多配一个后事件，配对后即移出队列。
func (m *Matcher) tryMatch(e Event, queue []Event, prev Event) (*Match, []Event) {
	if len(queue) == 0 {
		m.logger.Info("cep: no match",
			"key", e.Key, "second", e, "reason", "no pending first event")
		return nil, queue
	}

	idx := -1
	switch m.cfg.Contiguity {
	case Relaxed:
		idx = 0
	case Strict:
		if prev.Type == m.cfg.FirstType && queue[len(queue)-1] == prev {
			idx = len(queue) - 1
		}
	}
	if idx < 0 {
		m.logger.Info("cep: no match",
			"key", e.Key, "second", e, "mode", m.cfg.Contiguity,
			"reason", "strict contiguity broken: previous event is not a pending first")
		return nil, queue
	}

	first := queue[idx]
	delta := e.Timestamp - first.Timestamp
	if delta > m.cfg.MaxWindow {
		m.logger.Info("cep: no match",
			"key", e.Key, "first", first, "second", e,
			"delta", delta, "max_window", m.cfg.MaxWindow,
			"reason", "window exceeded")
		return nil, queue
	}

	match := &Match{Key: e.Key, First: first, Second: e, Delta: delta}
	m.logger.Info("cep: matched",
		"key", e.Key, "first", first, "second", e,
		"delta", delta, "max_window", m.cfg.MaxWindow,
		"mode", m.cfg.Contiguity, "reason", "within inclusive window")
	return match, append(queue[:idx], queue[idx+1:]...)
}

// Matches 返回已输出配对的副本，可并发安全读取。
func (m *Matcher) Matches() []Match {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Match, len(m.matches))
	copy(out, m.matches)
	return out
}

// PendingLen 返回指定键当前待匹配先事件的数量。
func (m *Matcher) PendingLen(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pending[key])
}
