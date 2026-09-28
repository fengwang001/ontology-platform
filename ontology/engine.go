package ontology

import (
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
)

// RejectReason 标识提交被拒绝的可区分原因。
type RejectReason string

const (
	RejectEmptyKey         RejectReason = "empty_key"
	RejectSeqNotIncreasing RejectReason = "seq_not_increasing"
	RejectBufferFull       RejectReason = "buffer_full"
)

// RejectError 描述一次被拒绝的提交及其判定依据。
type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string { return string(e.Reason) }

// ErrReject 便于 errors.Is 判断。
var ErrReject = errors.New("submit rejected")

func (e *RejectError) Unwrap() error { return ErrReject }

// Config 配置变更引擎。
//
// MaxAttempts 为单个事件的最大尝试次数：提交时立即尝试计为第 1 次，
// 之后每次 Advance 对阻塞键队首再尝试 1 次。达到上限仍失败则进入死信。
// BufferLimit 为所有键的待处理事件（阻塞队首 + FIFO 缓冲）总数上限。
type Config struct {
	MaxAttempts int
	BufferLimit int
	Logger      *log.Logger
}

// Engine 是按键有序、故障隔离的变更应用组件。
type Engine struct {
	mu       sync.Mutex
	applier  Applier
	maxTry   int
	bufLimit int
	logger   *log.Logger
	keys     map[string]*keyState
	order    []string
	applied  []Event
	dead     []Event
}

type keyState struct {
	blocked bool
	head    Event
	tried   int
	buffer  []Event
	lastSeq int64
	hasSeq  bool
}

func (s *keyState) pending() int {
	if s.blocked {
		return 1 + len(s.buffer)
	}
	return len(s.buffer)
}

// NewEngine 创建按键有序、故障隔离的变更应用引擎。
func NewEngine(applier Applier, cfg Config) *Engine {
	if cfg.MaxAttempts < 1 {
		cfg.MaxAttempts = 1
	}
	if cfg.BufferLimit < 0 {
		cfg.BufferLimit = 0
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[ontology] ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Engine{
		applier:  applier,
		maxTry:   cfg.MaxAttempts,
		bufLimit: cfg.BufferLimit,
		logger:   logger,
		keys:     map[string]*keyState{},
	}
}

// Submit 按事件所属键提交变更。
// 拒绝（空键 / 序号不严格递增 / 缓冲总数超限）不会改变任何键的状态、
// 尝试次数、死信或已应用列表。
func (e *Engine) Submit(ev Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 判定阶段全部为只读检查，保证被拒绝时不产生任何状态变更，
	// 且检查次序固定：空键 -> 序号 -> 缓冲上限。
	if ev.Key == "" {
		e.logger.Printf("submit reject input=%v reason=%s basis=%q", ev, RejectEmptyKey, "key must not be empty")
		return &RejectError{Reason: RejectEmptyKey}
	}
	st := e.keys[ev.Key]
	if st != nil && st.hasSeq && ev.Seq <= st.lastSeq {
		e.logger.Printf("submit reject input=%v reason=%s basis=%q", ev, RejectSeqNotIncreasing,
			fmt.Sprintf("seq %d <= last accepted seq %d for key %q", ev.Seq, st.lastSeq, ev.Key))
		return &RejectError{Reason: RejectSeqNotIncreasing}
	}
	if e.pendingTotal() >= e.bufLimit {
		e.logger.Printf("submit reject input=%v reason=%s basis=%q", ev, RejectBufferFull,
			fmt.Sprintf("pending total %d >= buffer limit %d", e.pendingTotal(), e.bufLimit))
		return &RejectError{Reason: RejectBufferFull}
	}

	// 通过校验后才允许修改状态。
	if st == nil {
		st = &keyState{}
		e.keys[ev.Key] = st
		e.order = append(e.order, ev.Key)
	}
	st.lastSeq = ev.Seq
	st.hasSeq = true

	if st.blocked {
		st.buffer = append(st.buffer, ev)
		e.logger.Printf("submit accepted input=%v decision=buffered basis=%q", ev,
			fmt.Sprintf("key %q is blocked; buffered FIFO depth %d", ev.Key, len(st.buffer)))
		return nil
	}

	st.tried = 1
	if err := e.applier.Apply(ev); err != nil {
		st.blocked = true
		st.head = ev
		e.logger.Printf("submit accepted input=%v decision=blocked attempt=%d basis=%q", ev, st.tried,
			"immediate apply on unblocked key failed: "+err.Error())
		return nil
	}
	st.tried = 0
	e.applied = append(e.applied, ev)
	e.logger.Printf("submit accepted input=%v decision=applied attempt=%d basis=%q", ev, 1,
		"immediate apply on unblocked key succeeded")
	return nil
}

// Advance 对每个阻塞键的队首事件再尝试一次。
// 达到最大尝试次数仍失败的队首进入死信，该键解除阻塞并按 FIFO 排空缓冲：
// 缓冲中的后续事件依次立即尝试，首个再次失败的事件成为新队首并重新阻塞。
// 阻塞键之间互不影响，按各键首次出现的固定次序推进。
func (e *Engine) Advance() {
	e.mu.Lock()
	defer e.mu.Unlock()

	for _, key := range e.order {
		st := e.keys[key]
		if !st.blocked {
			continue
		}
		e.retryHead(st)
		for !st.blocked && len(st.buffer) > 0 {
			next := st.buffer[0]
			st.buffer = st.buffer[1:]
			st.tried = 1
			if err := e.applier.Apply(next); err != nil {
				st.blocked = true
				st.head = next
				e.logger.Printf("advance drain input=%v decision=blocked attempt=%d basis=%q", next, st.tried,
					"buffered event failed while draining: "+err.Error())
				break
			}
			st.tried = 0
			e.applied = append(e.applied, next)
			e.logger.Printf("advance drain input=%v decision=applied attempt=%d basis=%q", next, 1,
				"buffered event applied while draining")
		}
	}
}

func (e *Engine) retryHead(st *keyState) {
	st.tried++
	attempt := st.tried
	err := e.applier.Apply(st.head)
	if err == nil {
		ev := st.head
		st.blocked = false
		st.head = Event{}
		st.tried = 0
		e.applied = append(e.applied, ev)
		e.logger.Printf("advance retry input=%v decision=applied attempt=%d basis=%q", ev, attempt,
			"blocked head retry succeeded")
		return
	}
	if attempt < e.maxTry {
		e.logger.Printf("advance retry input=%v decision=blocked attempt=%d basis=%q", st.head, attempt,
			fmt.Sprintf("head retry failed: %s; attempt %d < max %d", err.Error(), attempt, e.maxTry))
		return
	}
	ev := st.head
	st.blocked = false
	st.head = Event{}
	st.tried = 0
	e.dead = append(e.dead, ev)
	e.logger.Printf("advance retry input=%v decision=dead_letter attempt=%d basis=%q", ev, attempt,
		fmt.Sprintf("head retry failed: %s; attempt %d reached max %d", err.Error(), attempt, e.maxTry))
}

func (e *Engine) pendingTotal() int {
	total := 0
	for _, st := range e.keys {
		total += st.pending()
	}
	return total
}

// Applied 返回成功应用事件的快照副本，顺序即全局定案次序。
func (e *Engine) Applied() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.applied...)
}

// DeadLetters 返回死信事件的快照副本，顺序即进入死信的次序。
func (e *Engine) DeadLetters() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Event(nil), e.dead...)
}
