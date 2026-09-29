package cep

import (
	"fmt"
	"log/slog"
	"sync"
)

// Matcher 是复杂事件模式匹配组件：在同一键的事件流中找出
// 满足时间窗口的“先事件 -> 后事件”配对。
// 所有方法均可在多 goroutine 下并发调用。
type Matcher struct {
	mu      sync.RWMutex
	cfg     Config
	logger  *slog.Logger
	matches []Match

	// 以下字段仅在写锁内修改，且整批处理通过克隆-提交保证原子性。
	last    map[string]Event   // 每个键最近一次已提交的事件（单调性校验 / 严格连续）
	pending map[string][]Event // 宽松连续：每个键待匹配的先事件队列
}

// NewMatcher 校验参数并构造匹配器；参数非法时返回可区分的错误。
func NewMatcher(cfg Config, logger *slog.Logger) (*Matcher, error) {
	if cfg.FirstType == "" || cfg.SecondType == "" {
		return nil, fmt.Errorf("%w: first=%q second=%q", ErrEmptyEventType, cfg.FirstType, cfg.SecondType)
	}
	if cfg.FirstType == cfg.SecondType {
		return nil, fmt.Errorf("%w: %q", ErrSameEventType, cfg.FirstType)
	}
	if cfg.Window <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidWindow, cfg.Window)
	}
	if cfg.MaxPending <= 0 {
		return nil, fmt.Errorf("%w: %d", ErrInvalidMaxPending, cfg.MaxPending)
	}
	if cfg.Mode != RelaxedContiguity && cfg.Mode != StrictContiguity {
		return nil, fmt.Errorf("%w: %d", ErrInvalidMode, cfg.Mode)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Matcher{
		cfg:     cfg,
		logger:  logger,
		last:    make(map[string]Event),
		pending: make(map[string][]Event),
	}, nil
}

// ProcessBatch 原子地处理一批事件：全部校验通过才提交，
// 任一事件非法则整批拒绝，队列、上一事件与已输出配对均不变。
// 返回本批新产生的配对（按输入顺序）。
func (m *Matcher) ProcessBatch(events []Event) ([]Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Info("process batch", "mode", m.cfg.Mode, "events", events)

	// 在克隆状态上试算，任何一步失败都直接丢弃，已提交状态不受影响。
	last := make(map[string]Event, len(m.last))
	for k, v := range m.last {
		last[k] = v
	}
	pending := make(map[string][]Event, len(m.pending))
	for k, v := range m.pending {
		pending[k] = append([]Event(nil), v...)
	}

	var produced []Match
	for i, e := range events {
		if err := m.step(last, pending, e, &produced); err != nil {
			m.logger.Warn("batch rejected",
				"index", i, "event", e, "reason", err)
			return nil, err
		}
	}

	m.last = last
	m.pending = pending
	m.matches = append(m.matches, produced...)
	return produced, nil
}

// step 在试算状态上处理单条事件。
func (m *Matcher) step(last map[string]Event, pending map[string][]Event, e Event, produced *[]Match) error {
	if e.Key == "" {
		return fmt.Errorf("%w: type=%q ts=%d", ErrEmptyKey, e.Type, e.Timestamp)
	}
	if e.Type == "" {
		return fmt.Errorf("%w: key=%q ts=%d", ErrEmptyEventType, e.Key, e.Timestamp)
	}
	if prev, ok := last[e.Key]; ok && e.Timestamp < prev.Timestamp {
		return fmt.Errorf("%w: key=%q ts=%d < last=%d",
			ErrNonMonotonicTime, e.Key, e.Timestamp, prev.Timestamp)
	}

	switch m.cfg.Mode {
	case StrictContiguity:
		m.stepStrict(last, e, produced)
	default:
		if err := m.stepRelaxed(pending, e, produced); err != nil {
			return err
		}
	}
	last[e.Key] = e
	return nil
}

// stepStrict 严格连续：后事件必须是同键内紧挨先事件的下一个事件。
// 其他键的事件互不影响；同键的任意事件都会刷新“上一事件”。
func (m *Matcher) stepStrict(last map[string]Event, e Event, produced *[]Match) {
	prev, ok := last[e.Key]
	if !ok || prev.Type != m.cfg.FirstType || e.Type != m.cfg.SecondType {
		m.logger.Debug("no match",
			"event", e, "basis", "previous event of same key is not an unmatched first event")
		return
	}
	elapsed := e.Timestamp - prev.Timestamp
	if elapsed > m.cfg.Window {
		m.logger.Debug("no match",
			"event", e, "first", prev, "elapsed", elapsed, "window", m.cfg.Window,
			"basis", "elapsed exceeds window")
		return
	}
	match := Match{Key: e.Key, First: prev, Second: e, Elapsed: elapsed}
	*produced = append(*produced, match)
	m.logger.Info("match",
		"mode", m.cfg.Mode, "match", match,
		"basis", "second event immediately follows first event within window")
}

// stepRelaxed 宽松连续：先事件进入待匹配队列，后事件消费队列中
// 所有仍在窗口内的先事件（一个先事件至多配对一个后事件）。
func (m *Matcher) stepRelaxed(pending map[string][]Event, e Event, produced *[]Match) error {
	queue := pending[e.Key]

	// 时间单调不减，窗口外的先事件从队首依次过期。
	kept := queue[:0]
	for _, p := range queue {
		if e.Timestamp-p.Timestamp > m.cfg.Window {
			m.logger.Debug("pending expired",
				"first", p, "now", e.Timestamp, "window", m.cfg.Window,
				"basis", "elapsed exceeds window")
			continue
		}
		kept = append(kept, p)
	}
	queue = kept

	switch e.Type {
	case m.cfg.FirstType:
		if len(queue)+1 > m.cfg.MaxPending {
			return fmt.Errorf("%w: key=%q size=%d limit=%d",
				ErrPendingLimitExceeded, e.Key, len(queue)+1, m.cfg.MaxPending)
		}
		queue = append(queue, e)
		m.logger.Debug("first event queued", "event", e, "pending", len(queue))
	case m.cfg.SecondType:
		for _, p := range queue {
			match := Match{Key: e.Key, First: p, Second: e, Elapsed: e.Timestamp - p.Timestamp}
			*produced = append(*produced, match)
			m.logger.Info("match",
				"mode", m.cfg.Mode, "match", match,
				"basis", "pending first event within window paired with second event")
		}
		queue = queue[:0]
	default:
		m.logger.Debug("no match", "event", e, "basis", "unrelated event type")
	}

	pending[e.Key] = queue
	return nil
}

// Matches 返回截至当前已输出的全部配对副本，可并发读取。
func (m *Matcher) Matches() []Match {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Match, len(m.matches))
	copy(out, m.matches)
	return out
}
