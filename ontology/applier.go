package ontology

import (
	"io"
	"log/slog"
	"sort"
	"sync"
)

// Event 是按键有序提交的变更事件。
type Event struct {
	Key  string
	Seq  int64
	Body string
}

// Outcome 标识事件的最终定案结果。
type Outcome string

const (
	OutcomeApplied Outcome = "applied"
	OutcomeDead    Outcome = "dead-letter"
)

// RejectReason 标识拒绝提交的可区分原因。
type RejectReason string

const (
	RejectEmptyKey         RejectReason = "empty-key"
	RejectSeqNotIncreasing RejectReason = "seq-not-increasing"
	RejectBufferLimit      RejectReason = "buffer-limit-exceeded"
)

// RejectError 表示提交被拒绝；被拒绝的提交不改变任何状态。
type RejectError struct {
	Reason RejectReason
	Event  Event
}

func (e *RejectError) Error() string { return string(e.Reason) }

// DeadRecord 是进入死信的事件记录。
type DeadRecord struct {
	Event    Event
	Attempts int
	Err      string
}

// AppliedRecord 是成功应用的事件记录。
type AppliedRecord struct {
	Event    Event
	Attempts int
}

// KeyStatus 是单个键的可观测状态快照。
type KeyStatus struct {
	Key      string
	Blocked  bool
	Head     *Event
	Attempts int
	Buffered int
	LastSeq  int64
	HasLast  bool
}

const (
	// DefaultMaxAttempts 是事件进入死信前的最大尝试次数（含首次）。
	DefaultMaxAttempts = 3
	// DefaultMaxBuffered 是所有键缓冲队列允许的事件总数上限。
	DefaultMaxBuffered = 1024
)

// Applier 按键有序、故障隔离地应用变更。
type Applier struct {
	mu          sync.Mutex
	maxAttempts int
	maxBuffered int
	applyFunc   func(Event) error
	log         *slog.Logger
	keys        map[string]*keyState
	applied     []AppliedRecord
	dead        []DeadRecord
	bufferedCnt int
}

type keyState struct {
	key      string
	blocked  bool
	head     *Event
	attempts int
	buffer   []Event
	lastSeq  int64
	hasLast  bool
}

// NewApplier 创建一个 Applier。
// maxAttempts 为事件进入死信前的最大尝试次数（含首次立即尝试）；
// maxBuffered 为所有键 FIFO 缓冲队列（不含各键队首）的总容量。
// 非正值分别回落到 DefaultMaxAttempts / DefaultMaxBuffered。
// applyFunc 必须确定、快速且不回调 Applier；它在内部锁外调用。
func NewApplier(maxAttempts, maxBuffered int, applyFunc func(Event) error) *Applier {
	return NewApplierWithLogger(maxAttempts, maxBuffered, applyFunc, slog.Default())
}

// NewApplierWithLogger 与 NewApplier 相同，但使用指定日志记录器；
// logger 为 nil 时丢弃日志。
func NewApplierWithLogger(maxAttempts, maxBuffered int, applyFunc func(Event) error, logger *slog.Logger) *Applier {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	if maxBuffered <= 0 {
		maxBuffered = DefaultMaxBuffered
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Applier{
		maxAttempts: maxAttempts,
		maxBuffered: maxBuffered,
		applyFunc:   applyFunc,
		log:         logger,
		keys:        make(map[string]*keyState),
	}
}

// Submit 提交一个事件。
// 未阻塞键的事件立即尝试应用；应用失败则该键进入阻塞，事件成为队首。
// 阻塞键的事件进入其 FIFO 缓冲。空键、同键序号不严格递增、缓冲总数
// 超限都会以 *RejectError 拒绝，且拒绝不改变任何状态、尝试次数、死信或
// 已应用列表。
func (a *Applier) Submit(ev Event) error {
	a.mu.Lock()
	if ev.Key == "" {
		a.logReject(ev, RejectEmptyKey, "key is empty")
		a.mu.Unlock()
		return &RejectError{Reason: RejectEmptyKey, Event: ev}
	}
	st := a.keys[ev.Key]
	if st != nil && st.hasLast && ev.Seq <= st.lastSeq {
		a.logReject(ev, RejectSeqNotIncreasing,
			"seq must be strictly greater than last accepted seq")
		a.mu.Unlock()
		return &RejectError{Reason: RejectSeqNotIncreasing, Event: ev}
	}
	if st != nil && st.blocked && a.bufferedCnt >= a.maxBuffered {
		a.logReject(ev, RejectBufferLimit,
			"total buffered events at capacity")
		a.mu.Unlock()
		return &RejectError{Reason: RejectBufferLimit, Event: ev}
	}
	// 校验全部通过，序号推进在调用 applyFunc 之前落定，以保证并发提交
	// 下逐键序号次序与串行参照一致。
	if st == nil {
		st = &keyState{key: ev.Key}
		a.keys[ev.Key] = st
	}
	st.lastSeq = ev.Seq
	st.hasLast = true

	if !st.blocked {
		st.head = &ev
		st.attempts = 1
		a.mu.Unlock()

		err := a.applyFunc(ev)

		a.mu.Lock()
		if err == nil {
			a.recordAppliedLocked(ev, 1)
			st.head = nil
			st.attempts = 0
			a.logResolve(ev, OutcomeApplied, 1, "",
				"first attempt succeeded on submit")
			a.drainLocked(st)
		} else {
			st.blocked = true
			if st.attempts >= a.maxAttempts {
				a.killHeadLocked(st, err)
			} else {
				a.logResolve(ev, Outcome("blocked"), st.attempts, err.Error(),
					"first attempt failed on submit; key blocked")
			}
		}
		a.mu.Unlock()
		return nil
	}

	st.buffer = append(st.buffer, ev)
	a.bufferedCnt++
	a.log.Info("submit buffered",
		"key", ev.Key, "seq", ev.Seq,
		"buffered_for_key", len(st.buffer),
		"buffered_total", a.bufferedCnt,
		"reason", "key blocked; event queued FIFO behind head")
	a.mu.Unlock()
	return nil
}

// Advance 对每个阻塞键的队首再尝试一次：成功则定案为已应用并按 FIFO
// 排空该键缓冲；达到最大尝试次数仍失败则队首转入死信、解除阻塞并按 FIFO
// 排空缓冲。键按字典序处理以保证确定性。
func (a *Applier) Advance() {
	a.mu.Lock()
	blockedKeys := make([]string, 0, len(a.keys))
	for key, st := range a.keys {
		if st.blocked {
			blockedKeys = append(blockedKeys, key)
		}
	}
	sort.Strings(blockedKeys)
	a.log.Info("advance begin", "blocked_keys", blockedKeys)
	for _, key := range blockedKeys {
		st := a.keys[key]
		// 前一个键排空时不会改变本键；此处仍按阻塞态与队首双重确认。
		if !st.blocked || st.head == nil {
			continue
		}
		ev := *st.head
		st.attempts++
		attempts := st.attempts
		a.mu.Unlock()

		err := a.applyFunc(ev)

		a.mu.Lock()
		switch {
		case err == nil:
			a.recordAppliedLocked(ev, attempts)
			st.head = nil
			st.attempts = 0
			st.blocked = false
			a.logResolve(ev, OutcomeApplied, attempts, "",
				"retry succeeded on advance")
			a.drainLocked(st)
		case attempts >= a.maxAttempts:
			a.killHeadLocked(st, err)
		default:
			a.logResolve(ev, Outcome("blocked"), attempts, err.Error(),
				"retry failed on advance; key remains blocked")
		}
	}
	a.log.Info("advance done")
	a.mu.Unlock()
}

// drainLocked 在持锁状态下按 FIFO 依次尝试键的缓冲事件，直到缓冲为空
// 或某个事件失败（该事件成为新队首，键保持阻塞，等待后续 Advance）。
func (a *Applier) drainLocked(st *keyState) {
	for len(st.buffer) > 0 {
		ev := st.buffer[0]
		st.buffer = st.buffer[1:]
		a.bufferedCnt--

		st.head = &ev
		st.attempts = 1
		a.mu.Unlock()

		err := a.applyFunc(ev)

		a.mu.Lock()
		if err == nil {
			a.recordAppliedLocked(ev, 1)
			st.head = nil
			st.attempts = 0
			a.logResolve(ev, OutcomeApplied, 1, "",
				"buffered event applied while draining")
			continue
		}
		st.blocked = true
		if st.attempts >= a.maxAttempts {
			a.killHeadLocked(st, err)
			continue
		}
		a.logResolve(ev, Outcome("blocked"), 1, err.Error(),
			"buffered event failed while draining; key blocked")
		return
	}
}

// killHeadLocked 在持锁状态下把队首转入死信、清空队首并解除阻塞，
// 随后按 FIFO 继续排空缓冲。
func (a *Applier) killHeadLocked(st *keyState, cause error) {
	ev := *st.head
	attempts := st.attempts
	a.dead = append(a.dead, DeadRecord{
		Event:    ev,
		Attempts: attempts,
		Err:      cause.Error(),
	})
	a.logResolve(ev, OutcomeDead, attempts, cause.Error(),
		"attempts exhausted; moved to dead letter; key unblocked")
	st.head = nil
	st.attempts = 0
	st.blocked = false
	a.drainLocked(st)
}

func (a *Applier) recordAppliedLocked(ev Event, attempts int) {
	a.applied = append(a.applied, AppliedRecord{Event: ev, Attempts: attempts})
}

func (a *Applier) logReject(ev Event, reason RejectReason, basis string) {
	a.log.Warn("submit rejected",
		"input_key", ev.Key, "input_seq", ev.Seq,
		"reason", string(reason), "basis", basis)
}

func (a *Applier) logResolve(ev Event, outcome Outcome, attempts int,
	applyErr, basis string) {
	attrs := []any{
		"key", ev.Key, "seq", ev.Seq,
		"outcome", string(outcome),
		"attempts", attempts,
		"basis", basis,
	}
	if applyErr != "" {
		attrs = append(attrs, "apply_error", applyErr)
	}
	a.log.Info("event resolved", attrs...)
}

// Applied 返回已成功应用的事件记录快照（按定案先后）。
func (a *Applier) Applied() []AppliedRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]AppliedRecord, len(a.applied))
	copy(out, a.applied)
	return out
}

// DeadLetters 返回死信记录快照（按转入先后）。
func (a *Applier) DeadLetters() []DeadRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]DeadRecord, len(a.dead))
	copy(out, a.dead)
	return out
}

// KeyStatuses 返回所有键的状态快照，按键字典序排列。
func (a *Applier) KeyStatuses() []KeyStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	keys := make([]string, 0, len(a.keys))
	for key := range a.keys {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]KeyStatus, 0, len(keys))
	for _, key := range keys {
		st := a.keys[key]
		ks := KeyStatus{
			Key:      st.key,
			Blocked:  st.blocked,
			Attempts: st.attempts,
			Buffered: len(st.buffer),
			LastSeq:  st.lastSeq,
			HasLast:  st.hasLast,
		}
		if st.head != nil {
			head := *st.head
			ks.Head = &head
		}
		out = append(out, ks)
	}
	return out
}
