package notify

import (
	"sync"

	"ontology/alertstore"
	"ontology/suppress"
)

// Notifier 是告警分组通知器，所有方法并发安全。
type Notifier struct {
	mu      sync.Mutex
	groups  []string
	wait    int64
	repeat  int64
	store   *alertstore.Store
	engine  *suppress.Engine
	maxNow  int64
	hasTime bool
	state   map[string]*groupState
}

type groupState struct {
	lastSent int64
	hasSent  bool
	lastSet  map[string]bool
}

// Config 是 Notifier 的构造参数（时间均为毫秒）。
type Config struct {
	GroupLabels []string
	Wait        int64
	Repeat      int64
	MaxAlerts   int
	Rules       []suppress.InhibitRule
}

// New 构造通知器。
func New(cfg Config) (*Notifier, error) {
	const maxInterval = int64(1_000_000_000)
	if cfg.Wait < 0 || cfg.Wait > maxInterval ||
		cfg.Repeat < 0 || cfg.Repeat > maxInterval ||
		cfg.MaxAlerts < 1 || cfg.MaxAlerts > 100_000 {
		return nil, ErrInvalidArgument
	}
	for _, rule := range cfg.Rules {
		if !validRule(rule) {
			return nil, ErrInvalidArgument
		}
	}
	groups := make([]string, len(cfg.GroupLabels))
	copy(groups, cfg.GroupLabels)
	rules := make([]suppress.InhibitRule, len(cfg.Rules))
	copy(rules, cfg.Rules)
	return &Notifier{
		groups: groups,
		wait:   cfg.Wait,
		repeat: cfg.Repeat,
		store:  alertstore.New(cfg.MaxAlerts),
		engine: suppress.New(rules),
		state:  map[string]*groupState{},
	}, nil
}

// Fire 接收一条触发告警。
func (n *Notifier) Fire(now int64, labels alertstore.Labels) (FireResult, error) {
	if !validMillis(now) || !validLabels(labels) {
		return FireResult{}, ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkClock(now); err != nil {
		return FireResult{}, err
	}
	a, deduped, err := n.store.Fire(now, labels)
	if err != nil {
		return FireResult{}, err
	}
	n.advanceClock(now)
	return FireResult{Alert: a, Deduped: deduped}, nil
}

// Resolve 解除一条告警。
func (n *Notifier) Resolve(now int64, labels alertstore.Labels) (*alertstore.Alert, error) {
	if !validMillis(now) || !validLabels(labels) {
		return nil, ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	a, err := n.store.Resolve(labels)
	if err != nil {
		return nil, err
	}
	n.advanceClock(now)
	return a, nil
}

// AddSilence 添加静默规则。
func (n *Notifier) AddSilence(now int64, id string, m suppress.Matcher, start, end int64) error {
	if !validMillis(now) || id == "" || !validMatcher(m) ||
		!validMillis(start) || !validMillis(end) || start >= end {
		return ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkClock(now); err != nil {
		return err
	}
	if err := n.engine.AddSilence(now, id, m, start, end); err != nil {
		return err
	}
	n.advanceClock(now)
	return nil
}

// ExpireSilence 提前结束静默。
func (n *Notifier) ExpireSilence(now int64, id string) error {
	if !validMillis(now) || id == "" {
		return ErrInvalidArgument
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.checkClock(now); err != nil {
		return err
	}
	if err := n.engine.ExpireSilence(now, id); err != nil {
		return err
	}
	n.advanceClock(now)
	return nil
}
