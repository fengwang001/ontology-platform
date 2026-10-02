// Package sigsub implements a multi-threaded process signal delivery subsystem.
package sigsub

import (
	"errors"
	"fmt"
	"math/bits"
	"sync"
)

const (
	MinSig      = 1
	MaxSig      = 64
	MaxStandard = 31
	// SigKill cannot be masked, ignored, or given an action.
	SigKill    = 9
	MaxThreads = 64
	MaxQuota   = 1000
)

var (
	ErrInvalidParam      = errors.New("sigsub: invalid parameter")
	ErrThreadNotExist    = errors.New("sigsub: thread does not exist")
	ErrTooManyThreads    = errors.New("sigsub: too many threads")
	ErrProcessTerminated = errors.New("sigsub: process terminated")
	ErrNoHandler         = errors.New("sigsub: no handler on stack")
	ErrAgain             = errors.New("sigsub: realtime queue quota exceeded")
)

var syncMask, defaultIgnoreMask uint64

func init() {
	for _, sig := range []int{4, 7, 8, 11} {
		syncMask |= bit(sig)
	}
	for _, sig := range []int{17, 23, 28} {
		defaultIgnoreMask |= bit(sig)
	}
}

// bit returns the mask bit for signal sig: bit position s (1-based) is signal s.
func bit(sig int) uint64 { return uint64(1) << uint(sig-1) }

// ActionKind is the kind of a signal action.
type ActionKind int

const (
	ActionDefault ActionKind = iota
	ActionIgnore
	ActionHandler
)

// Action describes the disposition of one signal.
type Action struct {
	Kind ActionKind
	// Handler is the handler function number (ActionHandler only).
	Handler int
	// Mask is OR-ed into the thread mask while the handler runs.
	Mask uint64
	// Nodefer suppresses masking of the delivered signal itself.
	Nodefer bool
}

func DefaultAction() Action { return Action{Kind: ActionDefault} }

func IgnoreAction() Action { return Action{Kind: ActionIgnore} }

func HandlerAction(handler int, mask uint64, nodefer bool) Action {
	return Action{Kind: ActionHandler, Handler: handler, Mask: mask, Nodefer: nodefer}
}

// PendingEntry is one (signal, entry count) pair of a pending list.
type PendingEntry struct {
	Sig   int
	Count int
}

// pendingSet is one pending set (shared or per-thread private).
// Standard signals are a bitmask (merged); realtime signals keep one FIFO
// queue per signal number carrying values.
type pendingSet struct {
	std    uint64
	rtBits uint64
	rt     [MaxSig + 1][]int64
	rtN    int
}

func (p *pendingSet) pendingBits() uint64 { return p.std | p.rtBits }

// pick selects the next unmasked signal using bit operations only:
// smallest synchronous signal first, otherwise smallest signal number.
func (p *pendingSet) pick(mask uint64) (int, bool) {
	avail := p.pendingBits() &^ mask
	if avail == 0 {
		return 0, false
	}
	if syn := avail & syncMask; syn != 0 {
		return bits.TrailingZeros64(syn) + 1, true
	}
	return bits.TrailingZeros64(avail) + 1, true
}

// enqueueStd marks a standard signal pending. Reports whether it merged.
func (p *pendingSet) enqueueStd(sig int) (merged bool) {
	if p.std&bit(sig) != 0 {
		return true
	}
	p.std |= bit(sig)
	return false
}

// enqueueRT appends one realtime entry.
func (p *pendingSet) enqueueRT(sig int, value int64) {
	p.rt[sig] = append(p.rt[sig], value)
	p.rtBits |= bit(sig)
	p.rtN++
}

// dequeue removes the selected signal (realtime: queue head).
func (p *pendingSet) dequeue(sig int) int64 {
	if sig <= MaxStandard {
		p.std &^= bit(sig)
		return 0
	}
	q := p.rt[sig]
	v := q[0]
	q = q[1:]
	p.rt[sig] = q
	if len(q) == 0 {
		p.rtBits &^= bit(sig)
	}
	p.rtN--
	return v
}

// flush removes every instance of sig from this set.
func (p *pendingSet) flush(sig int) (stdRemoved bool, rtRemoved int) {
	if sig <= MaxStandard {
		if p.std&bit(sig) != 0 {
			p.std &^= bit(sig)
			return true, 0
		}
		return false, 0
	}
	n := len(p.rt[sig])
	if n > 0 {
		p.rt[sig] = nil
		p.rtBits &^= bit(sig)
		p.rtN -= n
	}
	return false, n
}

// list returns the pending (signal, count) pairs in ascending order.
func (p *pendingSet) list() []PendingEntry {
	var out []PendingEntry
	m := p.pendingBits()
	for m != 0 {
		sig := bits.TrailingZeros64(m) + 1
		m &= m - 1
		if sig <= MaxStandard {
			out = append(out, PendingEntry{Sig: sig, Count: 1})
		} else {
			out = append(out, PendingEntry{Sig: sig, Count: len(p.rt[sig])})
		}
	}
	return out
}

type thread struct {
	mask    uint64
	private pendingSet
	stack   []uint64
}

// Sub is the signal delivery subsystem of one multi-threaded process.
// All methods are safe for concurrent use; the result is equivalent to
// some serial order of the calls.
type Sub struct {
	mu         sync.Mutex
	quota      int
	threads    []thread // index 0 is the leader (tid 1)
	actions    [MaxSig + 1]Action
	shared     pendingSet
	curr       int // target-selection cursor (a tid, 1-based)
	rtq        int // total realtime entries across all sets
	lost       uint64
	terminated bool

	// examined counts SetAction flush probes: at most (threads+1) plus
	// the number of flushed entries.
	examined int
	// Accounting counters for the conservation invariant:
	// enqueued == delivered + discarded + flushed + still pending.
	enqueued  uint64
	delivered uint64
	discarded uint64
	flushed   uint64
}

// New creates a subsystem with realtime queue quota quota (0..MaxQuota)
// and one leader thread (tid 1) with the given mask. The kill bit is
// always cleared from every mask.
func New(quota int, leaderMask uint64) (*Sub, error) {
	if quota < 0 || quota > MaxQuota {
		return nil, ErrInvalidParam
	}
	s := &Sub{quota: quota, curr: 1}
	s.threads = append(s.threads, thread{mask: leaderMask &^ bit(SigKill)})
	return s, nil
}

// SendResult is the outcome of Send or SendTo.
type SendResult int

const (
	// SendEnqueued means the signal was queued.
	SendEnqueued SendResult = iota
	// SendMerged means a standard signal was already pending in that set.
	SendMerged
	// SendDropped means the signal was discarded under an ignore disposition.
	SendDropped
)

func (r SendResult) String() string {
	switch r {
	case SendEnqueued:
		return "enqueued"
	case SendMerged:
		return "merged"
	case SendDropped:
		return "dropped"
	}
	return fmt.Sprintf("SendResult(%d)", int(r))
}

// DeliverKind is the kind of a Deliver outcome.
type DeliverKind int

const (
	// DeliverNone means nothing deliverable was pending.
	DeliverNone DeliverKind = iota
	// DeliverTerminated means the process terminated.
	DeliverTerminated
	// DeliverHandler means a handler function must run.
	DeliverHandler
)

// DeliverResult describes the outcome of one Deliver call.
type DeliverResult struct {
	Kind DeliverKind
	// Handler is the handler function number (DeliverHandler only).
	Handler int
	// Sig is the delivered (or terminating) signal.
	Sig int
	// Value is the queued realtime value (0 for standard signals).
	Value int64
	// Shared reports whether the signal came from the shared set.
	Shared bool
	// Discarded counts signals discarded by this Deliver call.
	Discarded int
}

func (d DeliverResult) String() string {
	return fmt.Sprintf("kind=%d handler=%d sig=%d value=%d shared=%v discarded=%d",
		d.Kind, d.Handler, d.Sig, d.Value, d.Shared, d.Discarded)
}

func (s *Sub) threadLocked(tid int) (*thread, error) {
	if tid < 1 || tid > len(s.threads) {
		return nil, ErrThreadNotExist
	}
	return &s.threads[tid-1], nil
}

// isIgnored reports the effective disposition: ignore action, or default
// action on a signal in the default-ignore set.
func (s *Sub) isIgnored(sig int) bool {
	a := s.actions[sig]
	switch a.Kind {
	case ActionIgnore:
		return true
	case ActionDefault:
		return defaultIgnoreMask&bit(sig) != 0
	}
	return false
}

// AddThread creates the next thread (tid 2, 3, ...) with the given mask.
// Threads are never removed.
func (s *Sub) AddThread(mask uint64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.threads) >= MaxThreads {
		return 0, ErrTooManyThreads
	}
	if s.terminated {
		return 0, ErrProcessTerminated
	}
	s.threads = append(s.threads, thread{mask: mask &^ bit(SigKill)})
	return len(s.threads), nil
}

// SetMask replaces the thread's mask wholesale (the kill bit is cleared).
// Inside a handler it replaces the current (handler) mask as well.
func (s *Sub) SetMask(tid int, mask uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.threadLocked(tid)
	if err != nil {
		return err
	}
	if s.terminated {
		return ErrProcessTerminated
	}
	t.mask = mask &^ bit(SigKill)
	return nil
}

// SetAction sets the action of sig. Setting an effective ignore
// disposition immediately flushes every pending instance of sig from the
// shared set and every private set, decrementing RTQ accordingly.
func (s *Sub) SetAction(sig int, a Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sig < MinSig || sig > MaxSig || sig == SigKill {
		return ErrInvalidParam
	}
	if s.terminated {
		return ErrProcessTerminated
	}
	s.actions[sig] = a
	if !s.isIgnored(sig) {
		return nil
	}
	s.examined = 0
	s.flushSetLocked(sig, &s.shared)
	for i := range s.threads {
		s.flushSetLocked(sig, &s.threads[i].private)
	}
	return nil
}

func (s *Sub) flushSetLocked(sig int, p *pendingSet) {
	stdRemoved, rtRemoved := p.flush(sig)
	s.examined += 1 + rtRemoved
	s.rtq -= rtRemoved
	s.flushed += uint64(rtRemoved)
	if stdRemoved {
		s.flushed++
	}
}

// pickTargetLocked selects the target thread for a process-level signal:
// the leader for SigKill or when the leader does not mask it; otherwise
// the first unmasked thread found by scanning cyclically from curr
// (curr is then set to it). Returns 0 when every thread masks the signal.
func (s *Sub) pickTargetLocked(sig int) int {
	if sig == SigKill || s.threads[0].mask&bit(sig) == 0 {
		return 1
	}
	n := len(s.threads)
	for i := 0; i < n; i++ {
		tid := (s.curr-1+i)%n + 1
		if s.threads[tid-1].mask&bit(sig) == 0 {
			s.curr = tid
			return tid
		}
	}
	return 0
}

// Send queues a process-level signal into the shared set and selects a
// target thread. It returns the outcome and the target tid (0 if none).
func (s *Sub) Send(sig int, value int64) (SendResult, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sig < MinSig || sig > MaxSig {
		return 0, 0, ErrInvalidParam
	}
	if s.terminated {
		return 0, 0, ErrProcessTerminated
	}
	if s.isIgnored(sig) && s.threads[0].mask&bit(sig) == 0 {
		return SendDropped, 0, nil
	}
	if sig <= MaxStandard {
		if s.shared.enqueueStd(sig) {
			s.lost++
			return SendMerged, 0, nil
		}
	} else {
		if s.rtq >= s.quota {
			return 0, 0, ErrAgain
		}
		s.shared.enqueueRT(sig, value)
		s.rtq++
	}
	s.enqueued++
	return SendEnqueued, s.pickTargetLocked(sig), nil
}

// SendTo queues a thread-level signal into the private set of tid.
// The drop check uses that thread's own mask; the target is tid itself.
func (s *Sub) SendTo(tid, sig int, value int64) (SendResult, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sig < MinSig || sig > MaxSig {
		return 0, 0, ErrInvalidParam
	}
	t, err := s.threadLocked(tid)
	if err != nil {
		return 0, 0, err
	}
	if s.terminated {
		return 0, 0, ErrProcessTerminated
	}
	if s.isIgnored(sig) && t.mask&bit(sig) == 0 {
		return SendDropped, 0, nil
	}
	if sig <= MaxStandard {
		if t.private.enqueueStd(sig) {
			s.lost++
			return SendMerged, 0, nil
		}
	} else {
		if s.rtq >= s.quota {
			return 0, 0, ErrAgain
		}
		t.private.enqueueRT(sig, value)
		s.rtq++
	}
	s.enqueued++
	return SendEnqueued, tid, nil
}

func (s *Sub) killPendingLocked() bool {
	if s.shared.std&bit(SigKill) != 0 {
		return true
	}
	for i := range s.threads {
		if s.threads[i].private.std&bit(SigKill) != 0 {
			return true
		}
	}
	return false
}

// Deliver delivers pending signals to thread tid. The private set is
// consulted before the shared set; within each set, unmasked synchronous
// signals (smallest number) win, then the smallest unmasked number.
// The dequeue-and-mask-push step is atomic with respect to other calls.
func (s *Sub) Deliver(tid int) (DeliverResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.threadLocked(tid)
	if err != nil {
		return DeliverResult{}, err
	}
	if s.terminated {
		return DeliverResult{}, ErrProcessTerminated
	}
	if s.killPendingLocked() {
		s.terminated = true
		return DeliverResult{Kind: DeliverTerminated, Sig: SigKill}, nil
	}
	discarded := 0
	for {
		shared := false
		sig, ok := t.private.pick(t.mask)
		if !ok {
			sig, ok = s.shared.pick(t.mask)
			shared = true
		}
		if !ok {
			return DeliverResult{Kind: DeliverNone, Discarded: discarded}, nil
		}
		var value int64
		if shared {
			value = s.shared.dequeue(sig)
		} else {
			value = t.private.dequeue(sig)
		}
		if sig > MaxStandard {
			s.rtq--
		}
		switch {
		case s.isIgnored(sig):
			discarded++
			s.discarded++
			continue
		case s.actions[sig].Kind == ActionDefault:
			s.terminated = true
			s.delivered++
			return DeliverResult{Kind: DeliverTerminated, Sig: sig, Discarded: discarded}, nil
		default:
			a := s.actions[sig]
			t.stack = append(t.stack, t.mask)
			nm := t.mask | a.Mask
			if !a.Nodefer {
				nm |= bit(sig)
			}
			t.mask = nm &^ bit(SigKill)
			s.delivered++
			return DeliverResult{
				Kind:      DeliverHandler,
				Handler:   a.Handler,
				Sig:       sig,
				Value:     value,
				Shared:    shared,
				Discarded: discarded,
			}, nil
		}
	}
}

// Sigreturn pops and restores the most recently pushed mask of tid.
func (s *Sub) Sigreturn(tid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.threadLocked(tid)
	if err != nil {
		return err
	}
	if s.terminated {
		return ErrProcessTerminated
	}
	n := len(t.stack)
	if n == 0 {
		return ErrNoHandler
	}
	t.mask = t.stack[n-1]
	t.stack = t.stack[:n-1]
	return nil
}

// Pending returns the private pending list of tid, ascending by signal.
func (s *Sub) Pending(tid int) ([]PendingEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.threadLocked(tid)
	if err != nil {
		return nil, err
	}
	return t.private.list(), nil
}

// SharedPending returns the shared pending list, ascending by signal.
func (s *Sub) SharedPending() []PendingEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shared.list()
}

// Mask returns the current mask of tid.
func (s *Sub) Mask(tid int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.threadLocked(tid)
	if err != nil {
		return 0, err
	}
	return t.mask, nil
}

// RTQ returns the total number of queued realtime entries.
func (s *Sub) RTQ() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rtq
}

// Curr returns the target-selection cursor.
func (s *Sub) Curr() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.curr
}

// Lost returns the number of merged standard signals.
func (s *Sub) Lost() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lost
}
