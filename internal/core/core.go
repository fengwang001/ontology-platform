// Package core implements the shared settlement engine behind the
// revenue, hold and payout facade packages. A single mutex serializes
// every operation, so concurrent calls are equivalent to some serial
// order.
package core

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MaxClock is the largest accepted value of now, inclusive.
const MaxClock = int64(1_000_000_000_000)

var (
	ErrInvalidParam    = errors.New("invalid parameter")
	ErrClockBack       = errors.New("clock rollback")
	ErrNoSplit         = errors.New("content has no split table")
	ErrEventConflict   = errors.New("event conflict")
	ErrEventNotFound   = errors.New("event not found")
	ErrAlreadyRefunded = errors.New("event already refunded")
	ErrHoldExists      = errors.New("hold already exists")
	ErrHoldNotFound    = errors.New("hold not found")
	ErrCreatorNotFound = errors.New("creator not found")
)

// Part is one creator's basis points in a split table.
type Part struct {
	Creator string
	Bps     int64
}

// ShareInfo is the public view of one allocated share.
type ShareInfo struct {
	Creator string
	Amount  int64
}

type share struct {
	owner     *creator
	ct        *content
	amount    int64
	remaining int64
	eventTime int64
	holds     int
	idx       int
	prev      *share
	next      *share
}

type event struct {
	content  string
	amount   int64
	refunded bool
	shares   []*share
	infos    []ShareInfo
}

type hold struct {
	ct       *content
	from, to int64
}

type content struct {
	parts []Part
	holds map[string]*hold
	live  map[*share]struct{}
}

// creator keeps live shares (remaining > 0) in a time-ordered list plus a
// Fenwick tree over unfrozen remaining, indexed by append-only event time.
type creator struct {
	debt    int64
	heldSum int64
	times   []int64
	vals    []int64
	bit     []int64
	head    *share
	tail    *share
}

// add applies delta to slot i, keeping the plain values and the Fenwick
// tree in sync.
func (c *creator) add(i int, delta int64) {
	c.vals[i] += delta
	for i++; i < len(c.bit); i += i & (-i) {
		c.bit[i] += delta
	}
}

// bitSum returns the sum of unfrozen remaining over indices [0, i).
func (c *creator) bitSum(i int) int64 {
	var s int64
	for ; i > 0; i -= i & (-i) {
		s += c.bit[i]
	}
	return s
}

// appendSlot grows the time index and Fenwick tree by one zero-valued
// share, initializing the new tree slot to its covered range sum.
func (c *creator) appendSlot(t int64) int {
	idx := len(c.times)
	c.times = append(c.times, t)
	c.vals = append(c.vals, 0)
	n := len(c.times)
	c.bit = append(c.bit, 0)
	var s int64
	for k := n - n&(-n); k < n; k++ {
		s += c.vals[k]
	}
	c.bit[n] = s
	return idx
}

type Engine struct {
	mu       sync.Mutex
	wd       int64
	min      int64
	maxNow   int64
	contents map[string]*content
	creators map[string]*creator
	events   map[string]*event
	holds    map[string]*hold
	touched  int
}

func New(wd, min int64) (*Engine, error) {
	if wd < 0 || wd > 1_000_000_000 || min < 1 || min > 1_000_000_000_000 {
		return nil, fmt.Errorf("new: %w", ErrInvalidParam)
	}
	return &Engine{
		wd:       wd,
		min:      min,
		maxNow:   -1,
		contents: make(map[string]*content),
		creators: make(map[string]*creator),
		events:   make(map[string]*event),
		holds:    make(map[string]*hold),
	}, nil
}

func (e *Engine) checkClock(now int64) error {
	if now < 0 || now > MaxClock {
		return fmt.Errorf("%w: now=%d", ErrInvalidParam, now)
	}
	if now < e.maxNow {
		return fmt.Errorf("%w: now=%d < %d", ErrClockBack, now, e.maxNow)
	}
	return nil
}

func (e *Engine) ensureCreator(name string) *creator {
	c := e.creators[name]
	if c == nil {
		c = &creator{bit: make([]int64, 1)} // slot 0 unused: Fenwick is 1-indexed
		e.creators[name] = c
	}
	return c
}

func (e *Engine) unlink(s *share) {
	cr := s.owner
	if s.prev != nil {
		s.prev.next = s.next
	} else {
		cr.head = s.next
	}
	if s.next != nil {
		s.next.prev = s.prev
	} else {
		cr.tail = s.prev
	}
	delete(s.ct.live, s)
	s.prev, s.next = nil, nil
}

// consume deducts exactly amount from the creator's mature unfrozen live
// shares in FIFO order, touching only consumed shares, held shares met on
// the way and at most one immature share.
func (e *Engine) consume(cr *creator, matureBefore, amount int64) {
	for s := cr.head; s != nil && amount > 0; {
		e.touched++
		if s.eventTime > matureBefore {
			break
		}
		next := s.next
		if s.holds == 0 {
			take := min(s.remaining, amount)
			s.remaining -= take
			amount -= take
			cr.add(s.idx, -take)
			if s.remaining == 0 {
				e.unlink(s)
			}
		}
		s = next
	}
}

func (e *Engine) SetSplit(now int64, contentID string, parts []Part) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if contentID == "" || len(parts) < 1 || len(parts) > 8 {
		return fmt.Errorf("setsplit: %w", ErrInvalidParam)
	}
	seen := make(map[string]bool, len(parts))
	var sum int64
	for _, p := range parts {
		if p.Creator == "" || p.Bps < 1 || p.Bps > 10000 || seen[p.Creator] {
			return fmt.Errorf("setsplit: %w", ErrInvalidParam)
		}
		seen[p.Creator] = true
		sum += p.Bps
	}
	if sum != 10000 {
		return fmt.Errorf("setsplit: %w: bps sum=%d", ErrInvalidParam, sum)
	}
	if err := e.checkClock(now); err != nil {
		return fmt.Errorf("setsplit: %w", err)
	}
	ct := e.contents[contentID]
	if ct == nil {
		ct = &content{holds: make(map[string]*hold), live: make(map[*share]struct{})}
		e.contents[contentID] = ct
	}
	ct.parts = append([]Part(nil), parts...)
	for _, p := range parts {
		e.ensureCreator(p.Creator)
	}
	e.maxNow = now
	return nil
}

func (e *Engine) Earn(now int64, eventID, contentID string, amount int64) ([]ShareInfo, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if eventID == "" || contentID == "" || amount < 1 || amount > 1_000_000_000_000 {
		return nil, fmt.Errorf("earn: %w", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return nil, fmt.Errorf("earn: %w", err)
	}
	ct := e.contents[contentID]
	if ct == nil {
		return nil, fmt.Errorf("earn: %w", ErrNoSplit)
	}
	if ev, ok := e.events[eventID]; ok {
		if ev.content == contentID && ev.amount == amount {
			return append([]ShareInfo(nil), ev.infos...), nil
		}
		return nil, fmt.Errorf("earn: %w", ErrEventConflict)
	}
	ev := &event{content: contentID, amount: amount}
	var sum int64
	for _, p := range ct.parts {
		a := amount * p.Bps / 10000
		sum += a
		ev.shares = append(ev.shares, &share{amount: a, remaining: a, eventTime: now})
		ev.infos = append(ev.infos, ShareInfo{Creator: p.Creator, Amount: a})
	}
	if rem := amount - sum; rem > 0 {
		ev.shares[0].amount += rem
		ev.shares[0].remaining += rem
		ev.infos[0].Amount += rem
	}
	for i, p := range ct.parts {
		s := ev.shares[i]
		cr := e.ensureCreator(p.Creator)
		s.owner = cr
		s.ct = ct
		for _, h := range ct.holds {
			if s.eventTime >= h.from && s.eventTime < h.to {
				s.holds++
			}
		}
		if s.remaining == 0 {
			continue
		}
		s.idx = cr.appendSlot(s.eventTime)
		if s.holds > 0 {
			cr.heldSum += s.remaining
		} else {
			cr.add(s.idx, s.remaining)
		}
		s.prev = cr.tail
		if cr.tail != nil {
			cr.tail.next = s
		} else {
			cr.head = s
		}
		cr.tail = s
		ct.live[s] = struct{}{}
	}
	e.events[eventID] = ev
	e.maxNow = now
	return append([]ShareInfo(nil), ev.infos...), nil
}

func (e *Engine) Hold(now int64, holdID, contentID string, from, to int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if holdID == "" || contentID == "" || from < 0 || from >= to || to > MaxClock {
		return fmt.Errorf("hold: %w", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return fmt.Errorf("hold: %w", err)
	}
	ct := e.contents[contentID]
	if ct == nil {
		return fmt.Errorf("hold: %w", ErrNoSplit)
	}
	if _, ok := e.holds[holdID]; ok {
		return fmt.Errorf("hold: %w", ErrHoldExists)
	}
	h := &hold{ct: ct, from: from, to: to}
	e.holds[holdID] = h
	ct.holds[holdID] = h
	for s := range ct.live {
		if s.eventTime >= from && s.eventTime < to {
			s.holds++
			if s.holds == 1 {
				s.owner.add(s.idx, -s.remaining)
				s.owner.heldSum += s.remaining
			}
		}
	}
	e.maxNow = now
	return nil
}

func (e *Engine) Release(now int64, holdID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if holdID == "" {
		return fmt.Errorf("release: %w", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return fmt.Errorf("release: %w", err)
	}
	h := e.holds[holdID]
	if h == nil {
		return fmt.Errorf("release: %w", ErrHoldNotFound)
	}
	delete(e.holds, holdID)
	delete(h.ct.holds, holdID)
	for s := range h.ct.live {
		if s.eventTime >= h.from && s.eventTime < h.to {
			s.holds--
			if s.holds == 0 {
				s.owner.heldSum -= s.remaining
				s.owner.add(s.idx, s.remaining)
			}
		}
	}
	e.maxNow = now
	return nil
}

// Balance is a read-only query; it never mutates state or the clock.
func (e *Engine) Balance(creatorID string, now int64) (held, pending, available, debt int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	cr := e.creators[creatorID]
	if cr == nil {
		return 0, 0, 0, 0
	}
	k := sort.Search(len(cr.times), func(i int) bool { return cr.times[i] > now-e.wd })
	available = cr.bitSum(k)
	pending = cr.bitSum(len(cr.times)) - available
	return cr.heldSum, pending, available, cr.debt
}

func (e *Engine) Settle(now int64, creatorID string) (payout, offset int64, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if creatorID == "" {
		return 0, 0, fmt.Errorf("settle: %w", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return 0, 0, fmt.Errorf("settle: %w", err)
	}
	cr := e.creators[creatorID]
	if cr == nil {
		return 0, 0, fmt.Errorf("settle: %w", ErrCreatorNotFound)
	}
	e.touched = 0
	matureBefore := now - e.wd
	k := sort.Search(len(cr.times), func(i int) bool { return cr.times[i] > matureBefore })
	available := cr.bitSum(k)
	debt := cr.debt
	if available <= debt {
		e.consume(cr, matureBefore, available)
		cr.debt = debt - available
		offset = available
	} else {
		e.consume(cr, matureBefore, debt)
		cr.debt = 0
		offset = debt
		if n := available - debt; n >= e.min {
			e.consume(cr, matureBefore, n)
			payout = n
		}
	}
	e.maxNow = now
	return payout, offset, nil
}

func (e *Engine) Refund(now int64, eventID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if eventID == "" {
		return fmt.Errorf("refund: %w", ErrInvalidParam)
	}
	if err := e.checkClock(now); err != nil {
		return fmt.Errorf("refund: %w", err)
	}
	ev := e.events[eventID]
	if ev == nil {
		return fmt.Errorf("refund: %w", ErrEventNotFound)
	}
	if ev.refunded {
		return fmt.Errorf("refund: %w", ErrAlreadyRefunded)
	}
	ev.refunded = true
	for _, s := range ev.shares {
		if paid := s.amount - s.remaining; paid > 0 {
			s.owner.debt += paid
		}
		if s.remaining > 0 {
			if s.holds > 0 {
				s.owner.heldSum -= s.remaining
			} else {
				s.owner.add(s.idx, -s.remaining)
			}
			e.unlink(s)
			s.remaining = 0
		}
	}
	e.maxNow = now
	return nil
}
