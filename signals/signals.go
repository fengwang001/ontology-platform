package signals

import (
	"errors"
	"math/bits"
	"sync"
)

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrNoSuchThread      = errors.New("no such thread")
	ErrTooManyThreads    = errors.New("too many threads")
	ErrProcessTerminated = errors.New("process terminated")
	ErrNoHandler         = errors.New("no handler")
	ErrAgain             = errors.New("resource temporarily unavailable")
)

type ActionKind int

const (
	ActionDefault ActionKind = iota
	ActionIgnore
	ActionHandler
)

type Action struct {
	Kind           ActionKind
	HandlerID      int
	AdditionalMask uint64
	NoDefer        bool
}

type EnqueueKind int

const (
	EnqueueDiscarded EnqueueKind = iota
	EnqueueMerged
	EnqueueQueued
)

type EnqueueResult struct {
	Kind         EnqueueKind
	TargetThread int
}

type DeliverKind int

const (
	DeliverNone DeliverKind = iota
	DeliverTerminated
	DeliverHandler
)

type DeliverResult struct {
	Kind       DeliverKind
	Signal     int
	Value      int
	HandlerID  int
	FromShared bool
	Discarded  int
}

type PendingItem struct {
	Signal int
	Count  int
}

type thread struct {
	mask       uint64
	maskStack  []uint64
	stdPending uint64
	rtMask     uint64
	rt         [65][]int
	rtCount    int
}

type pendingSet struct {
	std     uint64
	rtMask  uint64
	rt      [65][]int
	rtCount int
}

type Subsystem struct {
	mu                 sync.Mutex
	limit              int
	threads            []*thread
	shared             pendingSet
	actions            [65]Action
	curr               int
	terminated         bool
	lost               int
	examined           int
	enqueued           int
	delivered          int
	dropped            int
	immediatelyDropped int
	flushed            int
	killCount          int
}

const (
	minSignal  = 1
	maxSignal  = 64
	maxStdSig  = 31
	killSig    = 9
	maxThreads = 64
)

var (
	syncSignals        = bit(4) | bit(7) | bit(8) | bit(11)
	defaultIgnoredBits = bit(17) | bit(23) | bit(28)
	standardBits       = uint64(1)<<maxStdSig - 1
)

func bit(sig int) uint64 { return uint64(1) << (sig - 1) }

func validSignal(sig int) bool { return minSignal <= sig && sig <= maxSignal }

func sanitizeMask(mask uint64) uint64 { return mask &^ bit(killSig) }

func isStandard(sig int) bool { return sig <= maxStdSig }

func New(limit int, leaderMask uint64) (*Subsystem, error) {
	if limit < 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}

	s := &Subsystem{limit: limit, curr: 1}
	s.threads = []*thread{nil, {mask: sanitizeMask(leaderMask)}}
	return s, nil
}

func (s *Subsystem) AddThread(mask uint64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.threads) > maxThreads {
		return 0, ErrTooManyThreads
	}
	if s.terminated {
		return 0, ErrProcessTerminated
	}

	tid := len(s.threads)
	s.threads = append(s.threads, &thread{mask: sanitizeMask(mask)})
	return tid, nil
}

func (s *Subsystem) SetAction(sig int, action Action) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validSignal(sig) || sig == killSig ||
		action.Kind < ActionDefault || action.Kind > ActionHandler {
		return ErrInvalidArgument
	}
	if s.terminated {
		return ErrProcessTerminated
	}

	s.actions[sig] = action
	if s.effectivelyIgnored(sig) {
		s.flushSignal(sig)
	}
	return nil
}

func (s *Subsystem) SetMask(tid int, mask uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tid < 1 || tid >= len(s.threads) {
		return ErrNoSuchThread
	}
	if s.terminated {
		return ErrProcessTerminated
	}

	s.threads[tid].mask = sanitizeMask(mask)
	return nil
}

func (s *Subsystem) Send(sig int, value int) (EnqueueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validSignal(sig) {
		return EnqueueResult{}, ErrInvalidArgument
	}
	if s.terminated {
		return EnqueueResult{}, ErrProcessTerminated
	}

	leader := s.threads[1]
	if s.effectivelyIgnored(sig) && leader.mask&bit(sig) == 0 {
		s.immediatelyDropped++
		return EnqueueResult{Kind: EnqueueDiscarded}, nil
	}
	if isStandard(sig) {
		if s.shared.std&bit(sig) != 0 {
			s.lost++
			return EnqueueResult{Kind: EnqueueMerged}, nil
		}
	} else if s.shared.rtCount+s.totalPrivateRT() >= s.limit {
		return EnqueueResult{}, ErrAgain
	}

	s.enqueueShared(sig, value)
	target := 0
	if sig == killSig || leader.mask&bit(sig) == 0 {
		target = 1
	} else {
		n := len(s.threads) - 1
		for offset := 0; offset < n; offset++ {
			tid := 1 + (s.curr-1+offset)%n
			if s.threads[tid].mask&bit(sig) == 0 {
				target = tid
				s.curr = tid
				break
			}
		}
	}
	return EnqueueResult{Kind: EnqueueQueued, TargetThread: target}, nil
}

func (s *Subsystem) SendTo(tid, sig int, value int) (EnqueueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validSignal(sig) || tid < 1 {
		return EnqueueResult{}, ErrInvalidArgument
	}
	if tid >= len(s.threads) {
		return EnqueueResult{}, ErrNoSuchThread
	}
	if s.terminated {
		return EnqueueResult{}, ErrProcessTerminated
	}

	target := s.threads[tid]
	if s.effectivelyIgnored(sig) && target.mask&bit(sig) == 0 {
		s.immediatelyDropped++
		return EnqueueResult{Kind: EnqueueDiscarded}, nil
	}
	if isStandard(sig) {
		if target.stdPending&bit(sig) != 0 {
			s.lost++
			return EnqueueResult{Kind: EnqueueMerged}, nil
		}
	} else if s.shared.rtCount+s.totalPrivateRT() >= s.limit {
		return EnqueueResult{}, ErrAgain
	}

	s.enqueueThread(target, sig, value)
	return EnqueueResult{Kind: EnqueueQueued, TargetThread: tid}, nil
}

func (s *Subsystem) Deliver(tid int) (DeliverResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tid < 1 || tid >= len(s.threads) {
		return DeliverResult{}, ErrNoSuchThread
	}
	if s.terminated {
		return DeliverResult{}, ErrProcessTerminated
	}

	target := s.threads[tid]
	if s.hasKill() {
		s.consumeKill()
		s.delivered++
		s.terminated = true
		return DeliverResult{Kind: DeliverTerminated, Signal: killSig}, nil
	}

	discarded := 0
	for {
		sig, value, fromShared := s.takeForThread(target)
		if sig == 0 {
			return DeliverResult{Kind: DeliverNone, Discarded: discarded}, nil
		}

		action := s.actions[sig]
		switch {
		case s.effectivelyIgnored(sig):
			discarded++
			s.dropped++
		case action.Kind == ActionDefault:
			s.delivered++
			s.terminated = true
			return DeliverResult{Kind: DeliverTerminated, Signal: sig}, nil
		default:
			oldMask := target.mask
			newMask := oldMask | sanitizeMask(action.AdditionalMask)
			if !action.NoDefer {
				newMask |= bit(sig)
			}
			target.mask = sanitizeMask(newMask)
			target.maskStack = append(target.maskStack, oldMask)
			s.delivered++
			return DeliverResult{
				Kind:       DeliverHandler,
				Signal:     sig,
				Value:      value,
				HandlerID:  action.HandlerID,
				FromShared: fromShared,
				Discarded:  discarded,
			}, nil
		}
	}
}

func (s *Subsystem) Sigreturn(tid int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tid < 1 || tid >= len(s.threads) {
		return ErrNoSuchThread
	}
	if s.terminated {
		return ErrProcessTerminated
	}

	target := s.threads[tid]
	if len(target.maskStack) == 0 {
		return ErrNoHandler
	}

	last := len(target.maskStack) - 1
	target.mask = target.maskStack[last]
	target.maskStack = target.maskStack[:last]
	return nil
}

func (s *Subsystem) Pending(tid int) ([]PendingItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tid < 1 || tid >= len(s.threads) {
		return nil, ErrNoSuchThread
	}
	return s.pendingItemsThread(s.threads[tid]), nil
}

func (s *Subsystem) SharedPending() []PendingItem {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.pendingItemsShared()
}

func (s *Subsystem) Mask(tid int) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if tid < 1 || tid >= len(s.threads) {
		return 0, ErrNoSuchThread
	}
	return s.threads[tid].mask, nil
}

func (s *Subsystem) RTQ() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.shared.rtCount + s.totalPrivateRT()
}

func (s *Subsystem) Curr() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.curr
}

func (s *Subsystem) Lost() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.lost
}

func (s *Subsystem) Examined() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.examined
}

func (s *Subsystem) effectivelyIgnored(sig int) bool {
	action := s.actions[sig]
	return action.Kind == ActionIgnore ||
		(action.Kind == ActionDefault && defaultIgnoredBits&bit(sig) != 0)
}

func (s *Subsystem) enqueueShared(sig int, value int) {
	if isStandard(sig) {
		s.shared.std |= bit(sig)
		if sig == killSig {
			s.killCount++
		}
	} else {
		s.shared.rt[sig] = append(s.shared.rt[sig], value)
		s.shared.rtMask |= bit(sig)
		s.shared.rtCount++
	}
	s.enqueued++
}

func (s *Subsystem) enqueueThread(target *thread, sig int, value int) {
	if isStandard(sig) {
		target.stdPending |= bit(sig)
		if sig == killSig {
			s.killCount++
		}
	} else {
		target.rt[sig] = append(target.rt[sig], value)
		target.rtMask |= bit(sig)
		target.rtCount++
	}
	s.enqueued++
}

func (s *Subsystem) totalPrivateRT() int {
	total := 0
	for _, target := range s.threads[1:] {
		total += target.rtCount
	}
	return total
}

func (s *Subsystem) flushSignal(sig int) {
	s.examined++
	s.flushShared(sig)
	for _, target := range s.threads[1:] {
		s.examined++
		s.flushThread(target, sig)
	}
}

func (s *Subsystem) flushShared(sig int) {
	if isStandard(sig) {
		if s.shared.std&bit(sig) == 0 {
			return
		}
		s.shared.std &^= bit(sig)
		s.flushed++
		s.examined++
		if sig == killSig {
			s.killCount--
		}
		return
	}

	count := len(s.shared.rt[sig])
	if count == 0 {
		return
	}
	s.shared.rt[sig] = nil
	s.shared.rtMask &^= bit(sig)
	s.shared.rtCount -= count
	s.flushed += count
	s.examined += count
}

func (s *Subsystem) flushThread(target *thread, sig int) {
	if isStandard(sig) {
		if target.stdPending&bit(sig) == 0 {
			return
		}
		target.stdPending &^= bit(sig)
		s.flushed++
		s.examined++
		if sig == killSig {
			s.killCount--
		}
		return
	}

	count := len(target.rt[sig])
	if count == 0 {
		return
	}
	target.rt[sig] = nil
	target.rtMask &^= bit(sig)
	target.rtCount -= count
	s.flushed += count
	s.examined += count
}

func (s *Subsystem) hasKill() bool {
	return s.killCount > 0
}

func (s *Subsystem) consumeKill() {
	if s.shared.std&bit(killSig) != 0 {
		s.shared.std &^= bit(killSig)
		s.killCount--
		return
	}
	for _, target := range s.threads[1:] {
		if target.stdPending&bit(killSig) != 0 {
			target.stdPending &^= bit(killSig)
			s.killCount--
			return
		}
	}
}

func lowestSignal(mask uint64) int {
	return bits.TrailingZeros64(mask) + 1
}

func selectSignal(std, rt, blocked uint64) int {
	availableStd := std &^ blocked
	availableRT := rt &^ blocked
	if sync := availableStd & syncSignals; sync != 0 {
		return lowestSignal(sync)
	}
	if availableStd != 0 {
		stdSig := lowestSignal(availableStd)
		if availableRT == 0 {
			return stdSig
		}
		rtSig := lowestSignal(availableRT)
		if stdSig < rtSig {
			return stdSig
		}
		return rtSig
	}
	if availableRT != 0 {
		return lowestSignal(availableRT)
	}
	return 0
}

func (s *Subsystem) takeForThread(target *thread) (int, int, bool) {
	if sig := selectSignal(target.stdPending, target.rtMask, target.mask); sig != 0 {
		value := 0
		if !isStandard(sig) {
			value = popRT(&target.rt[sig], &target.rtMask, &target.rtCount, sig)
		} else {
			target.stdPending &^= bit(sig)
			if sig == killSig {
				s.killCount--
			}
		}
		return sig, value, false
	}

	if sig := selectSignal(s.shared.std, s.shared.rtMask, target.mask); sig != 0 {
		value := 0
		if !isStandard(sig) {
			value = popRT(&s.shared.rt[sig], &s.shared.rtMask, &s.shared.rtCount, sig)
		} else {
			s.shared.std &^= bit(sig)
			if sig == killSig {
				s.killCount--
			}
		}
		return sig, value, true
	}
	return 0, 0, false
}

func popRT(queue *[]int, mask *uint64, count *int, sig int) int {
	values := *queue
	value := values[0]
	if len(values) == 1 {
		*queue = nil
		*mask &^= bit(sig)
	} else {
		*queue = append([]int(nil), values[1:]...)
	}
	*count--
	return value
}

func (s *Subsystem) pendingItemsThread(target *thread) []PendingItem {
	items := make([]PendingItem, 0, bits.OnesCount64(target.stdPending|target.rtMask))
	for mask := target.stdPending & standardBits; mask != 0; {
		index := bits.TrailingZeros64(mask)
		sig := index + 1
		items = append(items, PendingItem{Signal: sig, Count: 1})
		mask &^= 1 << index
	}
	for mask := target.rtMask; mask != 0; {
		index := bits.TrailingZeros64(mask)
		sig := index + 1
		items = append(items, PendingItem{Signal: sig, Count: len(target.rt[sig])})
		mask &^= 1 << index
	}
	return items
}

func (s *Subsystem) pendingItemsShared() []PendingItem {
	items := make([]PendingItem, 0, bits.OnesCount64(s.shared.std|s.shared.rtMask))
	for mask := s.shared.std & standardBits; mask != 0; {
		index := bits.TrailingZeros64(mask)
		sig := index + 1
		items = append(items, PendingItem{Signal: sig, Count: 1})
		mask &^= 1 << index
	}
	for mask := s.shared.rtMask; mask != 0; {
		index := bits.TrailingZeros64(mask)
		sig := index + 1
		items = append(items, PendingItem{Signal: sig, Count: len(s.shared.rt[sig])})
		mask &^= 1 << index
	}
	return items
}
