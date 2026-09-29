// Package subscription 实现按谓词条件（键 >= 下界）过滤的订阅增量推送器。
//
// 每条变更在推送（Push）时获得一个连续递增、无洞的全局序号；订阅在注册
// （Subscribe）时记录生效位点，只有注册之后到达且命中条件的变更才会被推
// 送，注册之前已经投递过的历史变更不会补推。
package subscription

import (
	"errors"
	"log/slog"
	"strings"
	"sync"
)

// 三类互不相同、可判定的错误。失败操作不会改变任何状态。
var (
	// ErrInvalidSubscriptionID 表示订阅标识非法（空字符串或全空白）。
	ErrInvalidSubscriptionID = errors.New("subscription: invalid subscription id")
	// ErrDuplicateSubscription 表示同一订阅标识重复订阅。
	ErrDuplicateSubscription = errors.New("subscription: duplicate subscription id")
	// ErrSubscriptionNotFound 表示退订了一个未注册（或已退订）的标识。
	ErrSubscriptionNotFound = errors.New("subscription: subscription id not found")
)

// Change 是一条待推送的变更。Key 用于与订阅下界比较：Key >= LowerBound 命中。
type Change struct {
	Key   string
	Value string
}

// Notification 是投递给订阅者的一条增量通知。
type Notification struct {
	Seq        int64
	Key        string
	Value      string
	SubID      string
	LowerBound string
}

// Pusher 是并发安全的订阅增量推送器。
type Pusher struct {
	mu   sync.RWMutex
	log  *slog.Logger
	seq  int64                       // 已投递变更总数，也是下一个序号减一
	subs map[string]*subscriberState // 活跃订阅
}

// subscriberState 是单个订阅的内部状态。
type subscriberState struct {
	id         string
	lowerBound string
	startSeq   int64 // 注册生效位点：只有 Seq > startSeq 的变更可能被推送
	delivered  []Notification
}

// New 创建推送器。logger 为 nil 时使用 slog.Default。
func New(logger *slog.Logger) *Pusher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pusher{
		log:  logger,
		subs: make(map[string]*subscriberState),
	}
}

// Subscribe 注册订阅。id 必须非空且非全空白；lowerBound 为键下界（含等号）。
// 返回注册时的生效位点：该位点之前（含）已投递的变更不会补推。
func (p *Pusher) Subscribe(id, lowerBound string) (int64, error) {
	if strings.TrimSpace(id) == "" {
		p.log.Warn("subscribe rejected: invalid id",
			"id", id, "lowerBound", lowerBound, "reason", "empty or whitespace-only id",
			"error", ErrInvalidSubscriptionID)
		return 0, ErrInvalidSubscriptionID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.subs[id]; exists {
		p.log.Warn("subscribe rejected: duplicate id",
			"id", id, "lowerBound", lowerBound, "currentPosition", p.seq,
			"reason", "id already registered", "error", ErrDuplicateSubscription)
		return 0, ErrDuplicateSubscription
	}
	state := &subscriberState{
		id:         id,
		lowerBound: lowerBound,
		startSeq:   p.seq,
	}
	p.subs[id] = state
	p.log.Info("subscribe accepted",
		"id", id, "lowerBound", lowerBound, "startSeq", state.startSeq,
		"rule", "only changes with Seq > startSeq and Key >= lowerBound are pushed")
	return state.startSeq, nil
}

// Unsubscribe 退订。退订后该订阅不再收到任何推送。
func (p *Pusher) Unsubscribe(id string) error {
	if strings.TrimSpace(id) == "" {
		p.log.Warn("unsubscribe rejected: invalid id",
			"id", id, "reason", "empty or whitespace-only id",
			"error", ErrInvalidSubscriptionID)
		return ErrInvalidSubscriptionID
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, exists := p.subs[id]; !exists {
		p.log.Warn("unsubscribe rejected: id not registered",
			"id", id, "reason", "no active subscription with this id",
			"error", ErrSubscriptionNotFound)
		return ErrSubscriptionNotFound
	}
	delete(p.subs, id)
	p.log.Info("unsubscribe accepted", "id", id, "currentPosition", p.seq,
		"rule", "no further notifications are delivered after unsubscribe")
	return nil
}

// Push 为变更分配下一个连续序号，并投递给当前所有活跃且命中条件的订阅。
// 返回该变更的全局序号（从 1 开始）。
func (p *Pusher) Push(change Change) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seq++
	seq := p.seq
	matched := make([]string, 0)
	// map 迭代顺序在单次推送内不影响每个订阅自身的投递集合；序号分配与
	// 判定在同一把写锁内原子完成，因此每次运行结果都可复现。
	for id, state := range p.subs {
		// 注册生效位点之后到达（seq > startSeq）且键 >= 下界才命中。
		// startSeq 恒为注册时位点，而 seq 是注册之后才分配的新序号，
		// 故 seq > startSeq 对注册后推送恒成立；此处保留判定以明确语义。
		if seq > state.startSeq && change.Key >= state.lowerBound {
			state.delivered = append(state.delivered, Notification{
				Seq:        seq,
				Key:        change.Key,
				Value:      change.Value,
				SubID:      id,
				LowerBound: state.lowerBound,
			})
			matched = append(matched, id)
		}
	}
	p.log.Info("change pushed",
		"seq", seq, "key", change.Key, "value", change.Value,
		"matchedSubscribers", matched, "matchCount", len(matched),
		"rule", "hit iff Key >= lowerBound (equality included) and Seq > startSeq")
	return seq
}

// Position 返回已投递变更的总数（即当前全局位点），可并发读。
func (p *Pusher) Position() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.seq
}

// Snapshot 返回当前活跃订阅标识到其生效位点与下界的快照，可并发读。
type SnapshotEntry struct {
	StartSeq   int64
	LowerBound string
}

// Snapshot 返回活跃订阅快照（拷贝，调用方可自由修改）。
func (p *Pusher) Snapshot() map[string]SnapshotEntry {
	p.mu.RLock()
	defer p.mu.RUnlock()
	snap := make(map[string]SnapshotEntry, len(p.subs))
	for id, state := range p.subs {
		snap[id] = SnapshotEntry{StartSeq: state.startSeq, LowerBound: state.lowerBound}
	}
	return snap
}

// DeliveredChanges 返回某订阅自注册以来收到的全部通知（拷贝），可并发读。
func (p *Pusher) DeliveredChanges(id string) []Notification {
	p.mu.RLock()
	defer p.mu.RUnlock()
	state, ok := p.subs[id]
	if !ok {
		return nil
	}
	out := make([]Notification, len(state.delivered))
	copy(out, state.delivered)
	return out
}
