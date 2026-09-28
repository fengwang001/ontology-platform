// Package applier 实现按键有序、故障隔离的变更应用组件。
package applier

import (
	"errors"
	"log"
	"sort"
	"sync"
)

// 可区分的拒绝原因。
var (
	// ErrEmptyKey 表示提交的事件键为空。
	ErrEmptyKey = errors.New("applier: event key must not be empty")
	// ErrSequenceNotIncreasing 表示同键事件序号未严格递增。
	ErrSequenceNotIncreasing = errors.New("applier: sequence must be strictly increasing per key")
	// ErrBufferLimitExceeded 表示缓冲中的事件总数已达上限。
	ErrBufferLimitExceeded = errors.New("applier: buffered event limit exceeded")
)

// Event 是按键提交的变更事件。
type Event struct {
	Key     string
	Seq     int64
	Payload string
}

// ApplyFunc 应用一条事件，返回 error 表示本次尝试失败。
// 实现必须对同一条事件的重试保持幂等安全的语义由调用方负责。
type ApplyFunc func(Event) error

// Status 描述一次提交或重试的定案结果。
type Status string

const (
	StatusApplied  Status = "applied"
	StatusBuffered Status = "buffered"
	StatusRetrying Status = "retrying"
	StatusDead     Status = "dead"
)

// Outcome 是一条事件在一次操作中的定案结果。
type Outcome struct {
	Key      string
	Seq      int64
	Status   Status
	Attempts int
	Reason   string
}

// DeadEntry 记录一条进入死信的事件及其尝试次数。
type DeadEntry struct {
	Event    Event
	Attempts int
}

// Config 配置 Applier。
type Config struct {
	// MaxAttempts 是单条事件的最大尝试次数（含首次）。
	MaxAttempts int
	// MaxBuffered 是所有键上等待定案事件总数的上限。
	MaxBuffered int
}

const (
	defaultMaxAttempts = 3
	defaultMaxBuffered = 1024
)

// keyState 是单个键的全部状态；所有字段都在 Applier.mu 下访问。
type keyState struct {
	blocked  bool
	head     *Event
	attempts int
	lastSeq  int64
	hasSeq   bool
	buf      []Event
	applied  []Event
}

// Applier 按键有序、故障隔离地应用事件。
type Applier struct {
	apply   ApplyFunc
	cfg     Config
	mu      sync.Mutex
	keys    map[string]*keyState
	dead    []DeadEntry
	pending int
}

// New 创建 Applier。零值配置字段使用默认值：MaxAttempts=3，MaxBuffered=1024。
func New(cfg Config, apply ApplyFunc) *Applier {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = defaultMaxAttempts
	}
	if cfg.MaxBuffered <= 0 {
		cfg.MaxBuffered = defaultMaxBuffered
	}
	if apply == nil {
		apply = func(Event) error { return nil }
	}
	return &Applier{
		apply: apply,
		cfg:   cfg,
		keys:  make(map[string]*keyState),
	}
}

// Submit 提交一条事件。
//
// 校验失败时返回可区分的哨兵错误，且不改变任何状态：
// ErrEmptyKey、ErrSequenceNotIncreasing、ErrBufferLimitExceeded。
// 未阻塞键的事件立即尝试；阻塞键的事件按先进先出进入缓冲。
func (a *Applier) Submit(ev Event) (out Outcome, err error) {
	log.Printf("applier submit: input key=%q seq=%d payload=%q", ev.Key, ev.Seq, ev.Payload)
	if ev.Key == "" {
		log.Printf("applier submit: rejected key=%q seq=%d reason=%v", ev.Key, ev.Seq, ErrEmptyKey)
		return Outcome{}, ErrEmptyKey
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.keys[ev.Key]
	if st == nil {
		st = &keyState{}
		a.keys[ev.Key] = st
	}
	if st.hasSeq && ev.Seq <= st.lastSeq {
		log.Printf("applier submit: rejected key=%q seq=%d lastSeq=%d reason=%v",
			ev.Key, ev.Seq, st.lastSeq, ErrSequenceNotIncreasing)
		return Outcome{}, ErrSequenceNotIncreasing
	}
	if a.pending >= a.cfg.MaxBuffered {
		log.Printf("applier submit: rejected key=%q seq=%d pending=%d limit=%d reason=%v",
			ev.Key, ev.Seq, a.pending, a.cfg.MaxBuffered, ErrBufferLimitExceeded)
		return Outcome{}, ErrBufferLimitExceeded
	}

	st.lastSeq = ev.Seq
	st.hasSeq = true

	if st.blocked {
		st.buf = append(st.buf, ev)
		a.pending++
		out = Outcome{Key: ev.Key, Seq: ev.Seq, Status: StatusBuffered,
			Reason: "key blocked: event appended to FIFO buffer"}
		log.Printf("applier submit: decided key=%q seq=%d status=%s reason=%s",
			out.Key, out.Seq, out.Status, out.Reason)
		return out, nil
	}

	st.head = &ev
	st.attempts = 0
	a.pending++
	out = a.tryHeadLocked(st, ev.Key, "submit: immediate attempt on unblocked key")
	return out, nil
}

// Advance 对每个当前阻塞键的队首事件再尝试一次。
//
// 达到 MaxAttempts 仍失败的队首转入死信、解除该键阻塞，
// 并按先进先出依次尝试缓冲中的后续事件，直到再次失败阻塞或缓冲排空。
// 结果按键名排序返回，保证同样的状态下输出顺序确定。
func (a *Applier) Advance() []Outcome {
	a.mu.Lock()
	defer a.mu.Unlock()

	blocked := make([]string, 0, len(a.keys))
	for key, st := range a.keys {
		if st.blocked {
			blocked = append(blocked, key)
		}
	}
	sort.Strings(blocked)

	var outcomes []Outcome
	for _, key := range blocked {
		st := a.keys[key]
		if !st.blocked || st.head == nil {
			continue
		}
		out := a.tryHeadLocked(st, key, "advance: retry head of blocked key")
		outcomes = append(outcomes, out)
		if out.Status == StatusDead {
			outcomes = append(outcomes, a.drainLocked(st, key)...)
		}
	}
	log.Printf("applier advance: decided %d outcome(s) across %d blocked key(s)", len(outcomes), len(blocked))
	return outcomes
}

// tryHeadLocked 在持锁状态下尝试键的队首事件一次，并据结果推进状态。
// 调用前 st.head 必须非空。
func (a *Applier) tryHeadLocked(st *keyState, key, why string) Outcome {
	ev := *st.head
	st.attempts++
	attempt := st.attempts

	if err := a.apply(ev); err != nil {
		if attempt >= a.cfg.MaxAttempts {
			a.dead = append(a.dead, DeadEntry{Event: ev, Attempts: attempt})
			a.pending--
			out := Outcome{Key: key, Seq: ev.Seq, Status: StatusDead, Attempts: attempt,
				Reason: "attempts exhausted after failure: moved to dead-letter queue"}
			log.Printf("applier attempt: key=%q seq=%d attempt=%d applyErr=%v -> status=%s (%s)",
				key, ev.Seq, attempt, err, out.Status, why)
			st.head = nil
			st.attempts = 0
			st.blocked = false
			return out
		}
		st.blocked = true
		out := Outcome{Key: key, Seq: ev.Seq, Status: StatusRetrying, Attempts: attempt,
			Reason: "attempt failed: key blocked awaiting retry"}
		log.Printf("applier attempt: key=%q seq=%d attempt=%d applyErr=%v -> status=%s (%s)",
			key, ev.Seq, attempt, err, out.Status, why)
		return out
	}

	st.applied = append(st.applied, ev)
	a.pending--
	out := Outcome{Key: key, Seq: ev.Seq, Status: StatusApplied, Attempts: attempt,
		Reason: "attempt succeeded: event applied in key order"}
	log.Printf("applier attempt: key=%q seq=%d attempt=%d -> status=%s (%s)",
		key, ev.Seq, attempt, out.Status, why)
	st.head = nil
	st.attempts = 0
	st.blocked = false
	return out
}

// drainLocked 在队首进入死信后，按先进先出依次尝试缓冲事件。
func (a *Applier) drainLocked(st *keyState, key string) []Outcome {
	var outcomes []Outcome
	for len(st.buf) > 0 {
		next := st.buf[0]
		st.buf = st.buf[1:]
		st.head = &next
		st.attempts = 0
		out := a.tryHeadLocked(st, key, "drain: FIFO buffer after dead-letter")
		outcomes = append(outcomes, out)
		if st.blocked {
			break
		}
		if out.Status == StatusDead {
			continue
		}
	}
	return outcomes
}

// Applied 返回某键已成功应用事件的有序副本。
func (a *Applier) Applied(key string) []Event {
	a.mu.Lock()
	defer a.mu.Unlock()
	st := a.keys[key]
	if st == nil || len(st.applied) == 0 {
		return []Event{}
	}
	return append([]Event(nil), st.applied...)
}

// DeadLetters 返回死信条目的副本（按定案先后）。
func (a *Applier) DeadLetters() []DeadEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.dead) == 0 {
		return []DeadEntry{}
	}
	return append([]DeadEntry(nil), a.dead...)
}

// Blocked 返回当前阻塞键的有序副本。
func (a *Applier) Blocked() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	blocked := make([]string, 0)
	for key, st := range a.keys {
		if st.blocked {
			blocked = append(blocked, key)
		}
	}
	sort.Strings(blocked)
	return blocked
}

// Pending 返回所有键上等待定案的事件总数（队首 + 缓冲）。
func (a *Applier) Pending() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.pending
}
