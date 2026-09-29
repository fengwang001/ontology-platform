package ontology

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// RejectReason 标识一次提交被拒绝的可区分原因。
type RejectReason string

const (
	// RejectEmptyKey 表示事件的键为空。
	RejectEmptyKey RejectReason = "empty_key"
	// RejectSeqNotIncreasing 表示同键事件序号未严格递增。
	RejectSeqNotIncreasing RejectReason = "seq_not_strictly_increasing"
	// RejectBufferLimit 表示全局缓冲队列总数已达上限。
	RejectBufferLimit RejectReason = "buffer_limit_exceeded"
)

// Outcome 是事件的定案结果。
type Outcome string

const (
	// OutcomeApplied 表示事件已成功应用。
	OutcomeApplied Outcome = "applied"
	// OutcomeBuffered 表示事件因键阻塞而进入 FIFO 缓冲。
	OutcomeBuffered Outcome = "buffered"
	// OutcomeDeadLettered 表示事件重试达到上限仍失败，转入死信。
	OutcomeDeadLettered Outcome = "dead_lettered"
)

// Event 是按键提交、按序号定序的变更事件。
type Event struct {
	Key     string
	Seq     uint64
	Payload any
}

// DeadLetter 记录达到重试上限仍失败的事件及其已尝试次数。
type DeadLetter struct {
	Event    Event
	Attempts int
}

// Settlement 是一次事件定案（应用或死信）的记录。
type Settlement struct {
	Event    Event
	Outcome  Outcome
	Attempts int
	Basis    string
}

// SubmitResult 是一次提交的处理结果；被拒绝时 Accepted 为 false 且 Reason 给出原因。
type SubmitResult struct {
	Accepted   bool
	Reason     RejectReason
	Settlements []Settlement
}

// Applier 应用单个事件，返回非 nil 即视为本次尝试失败。
type Applier func(Event) error

// Processor 是按键有序、故障隔离的变更应用组件。
type Processor struct {
	mu          sync.Mutex
	maxAttempts int
	maxBuffered int
	apply       Applier
	logw        io.Writer

	buffered  int
	keys      map[string]*keyState
	blocked   []*keyState
	applied   []Event
	dead      []DeadLetter
}

// NewProcessor 创建处理器：maxAttempts 为每事件最大尝试次数（含首次），
// maxBuffered 为所有键的 FIFO 缓冲条目总数上限。
func NewProcessor(maxAttempts, maxBuffered int, apply Applier, logw io.Writer) (*Processor, error) {
	if maxAttempts < 1 {
		return nil, fmt.Errorf("maxAttempts must be >= 1, got %d", maxAttempts)
	}
	if maxBuffered < 0 {
		return nil, fmt.Errorf("maxBuffered must be >= 0, got %d", maxBuffered)
	}
	if apply == nil {
		return nil, fmt.Errorf("apply must not be nil")
	}
	if logw == nil {
		logw = io.Discard
	}
	return &Processor{
		maxAttempts: maxAttempts,
		maxBuffered: maxBuffered,
		apply:       apply,
		logw:        logw,
		keys:        make(map[string]*keyState),
	}, nil
}

type bufferedEvent struct {
	event Event
}

// keyState 是单个键的全部可变状态：是否阻塞、队首及其已尝试次数、FIFO 缓冲。
type keyState struct {
	key       string
	blocked   bool
	head      Event
	headTries int
	queue     []bufferedEvent
	lastSeq   uint64
	seen      bool
}

// Submit 提交一个事件。可被并发调用。
func (p *Processor) Submit(ctx context.Context, ev Event) SubmitResult {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logf("input Submit key=%q seq=%d", ev.Key, ev.Seq)

	if reason, basis := p.validate(ev); reason != "" {
		p.logf("decision rejected key=%q seq=%d reason=%s basis=%s", ev.Key, ev.Seq, reason, basis)
		return SubmitResult{Accepted: false, Reason: reason}
	}

	st := p.keys[ev.Key]
	if st == nil {
		st = &keyState{key: ev.Key}
		p.keys[ev.Key] = st
	}

	var settled []Settlement
	if st.blocked {
		st.queue = append(st.queue, bufferedEvent{event: ev})
		p.buffered++
		p.logf("decision buffered key=%q seq=%d basis=key_blocked_head_seq_%d_attempt_%d buffer_total=%d",
			ev.Key, ev.Seq, st.head.Seq, st.headTries, p.buffered)
		return SubmitResult{Accepted: true, Settlements: settled}
	}

	st.lastSeq = ev.Seq
	st.seen = true
	tries := 1
	if err := p.apply(ev); err != nil {
		st.blocked = true
		st.head = ev
		st.headTries = tries
		p.blocked = append(p.blocked, st)
		p.logf("decision blocked key=%q seq=%d attempt=%d basis=first_attempt_failed err=%q",
			ev.Key, ev.Seq, tries, err.Error())
		return SubmitResult{Accepted: true, Settlements: settled}
	}

	p.applied = append(p.applied, ev)
	settled = append(settled, Settlement{
		Event:    ev,
		Outcome:  OutcomeApplied,
		Attempts: tries,
		Basis:    "first_attempt_succeeded",
	})
	p.logf("decision applied key=%q seq=%d attempt=%d basis=first_attempt_succeeded",
		ev.Key, ev.Seq, tries)
	return SubmitResult{Accepted: true, Settlements: settled}
}

// Advance 对每个阻塞键的队首再尝试一次，并按规则排空或转死信。
func (p *Processor) Advance(ctx context.Context) []Settlement {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.logf("input Advance blocked_keys=%d", len(p.blocked))

	// 快照保证本次推进中每个键至多被推进一次：排空期间刚入队的事件
	// 不会让同一键在本轮被二次重试。
	snapshot := make([]*keyState, len(p.blocked))
	copy(snapshot, p.blocked)

	var settled []Settlement
	for _, st := range snapshot {
		select {
		case <-ctx.Done():
			return settled
		default:
		}
		if !st.blocked {
			continue
		}
		settled = append(settled, p.advanceKey(st)...)
	}

	for _, s := range settled {
		p.logf("decision settled key=%q seq=%d outcome=%s attempts=%d basis=%s",
			s.Event.Key, s.Event.Seq, s.Outcome, s.Attempts, s.Basis)
	}
	return settled
}

// Applied 返回按全局定案顺序排列的已应用事件快照。
func (p *Processor) Applied() []Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Event, len(p.applied))
	copy(out, p.applied)
	return out
}

// DeadLetters 返回按全局定案顺序排列的死信快照。
func (p *Processor) DeadLetters() []DeadLetter {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]DeadLetter, len(p.dead))
	copy(out, p.dead)
	return out
}

// BufferedCount 返回当前所有键 FIFO 缓冲中的事件总数。
func (p *Processor) BufferedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.buffered
}

// BlockedKeys 返回当前处于阻塞状态的键，按键名排序（稳定可复现）。
func (p *Processor) BlockedKeys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, 0, len(p.blocked))
	for _, st := range p.blocked {
		out = append(out, st.key)
	}
	sort.Strings(out)
	return out
}

// validate 在任何状态变更前检查全部拒绝条件；任一不满足都必须保持状态不变。
func (p *Processor) validate(ev Event) (RejectReason, string) {
	if strings.TrimSpace(ev.Key) == "" {
		return RejectEmptyKey, "event key must not be empty"
	}
	if st := p.keys[ev.Key]; st != nil && st.seen && ev.Seq <= st.lastSeq {
		return RejectSeqNotIncreasing, fmt.Sprintf(
			"seq %d must be strictly greater than last accepted seq %d", ev.Seq, st.lastSeq)
	}
	if p.buffered >= p.maxBuffered {
		return RejectBufferLimit, fmt.Sprintf(
			"buffer total %d already at limit %d", p.buffered, p.maxBuffered)
	}
	return "", ""
}

// advanceKey 对一个阻塞键的队首再尝试一次；若定案（成功或转死信），
// 则按 FIFO 排空缓冲，直到再次阻塞或缓冲耗尽。调用方须持有 p.mu。
func (p *Processor) advanceKey(st *keyState) []Settlement {
	var settled []Settlement

	st.headTries++
	err := p.apply(st.head)
	if err == nil {
		settled = append(settled, Settlement{
			Event:    st.head,
			Outcome:  OutcomeApplied,
			Attempts: st.headTries,
			Basis:    fmt.Sprintf("retry_attempt_%d_succeeded", st.headTries),
		})
		p.applied = append(p.applied, st.head)
		st.head = Event{}
		st.headTries = 0
		// 队首成功：解除阻塞并按 FIFO 排空缓冲。
		p.unblock(st)
		settled = append(settled, p.drainQueue(st)...)
		return settled
	}

	if st.headTries < p.maxAttempts {
		p.logf("advance retry_failed key=%q seq=%d attempt=%d basis=retry_failed_below_limit err=%q",
			st.key, st.head.Seq, st.headTries, err.Error())
		return settled
	}

	// 达到上限仍失败：队首转死信、解除阻塞，然后按 FIFO 排空缓冲。
	settled = append(settled, Settlement{
		Event:    st.head,
		Outcome:  OutcomeDeadLettered,
		Attempts: st.headTries,
		Basis:    fmt.Sprintf("retry_attempt_%d_reached_limit_%d", st.headTries, p.maxAttempts),
	})
	p.dead = append(p.dead, DeadLetter{Event: st.head, Attempts: st.headTries})
	st.head = Event{}
	st.headTries = 0
	p.unblock(st)
	settled = append(settled, p.drainQueue(st)...)
	return settled
}

// drainQueue 按 FIFO 应用该键缓冲中的事件；任一失败则该事件成为新队首、
// 键重新阻塞，剩余事件保留在缓冲中。调用方须持有 p.mu。
func (p *Processor) drainQueue(st *keyState) []Settlement {
	var settled []Settlement
	for len(st.queue) > 0 {
		item := st.queue[0]
		st.queue = st.queue[1:]
		p.buffered--

		tries := 1
		if err := p.apply(item.event); err != nil {
			st.blocked = true
			st.head = item.event
			st.headTries = tries
			p.blocked = append(p.blocked, st)
			p.logf("drain blocked key=%q seq=%d attempt=%d basis=drain_attempt_failed err=%q",
				st.key, item.event.Seq, tries, err.Error())
			return settled
		}
		settled = append(settled, Settlement{
			Event:    item.event,
			Outcome:  OutcomeApplied,
			Attempts: tries,
			Basis:    "drain_first_attempt_succeeded",
		})
		p.applied = append(p.applied, item.event)
	}
	return settled
}

// unblock 解除键的阻塞并从阻塞键列表中移除（保序）。调用方须持有 p.mu。
func (p *Processor) unblock(st *keyState) {
	st.blocked = false
	for i, b := range p.blocked {
		if b == st {
			p.blocked = append(p.blocked[:i], p.blocked[i+1:]...)
			return
		}
	}
}

func (p *Processor) logf(format string, args ...any) {
	fmt.Fprintf(p.logw, format+"\n", args...)
}
