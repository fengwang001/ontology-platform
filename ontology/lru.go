// Package ontology provides a two-segment (young/old) buffered-pool LRU
// manager with midpoint insertion and residence-time promotion.
package ontology

import (
	"errors"
	"sync"

	"container/list"
)

// Distinguishable rejection reasons, reported in the documented priority order.
var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrClockSkew       = errors.New("ontology: clock went backwards")
	ErrNotFound        = errors.New("ontology: page not in pool")
	ErrPinUnderflow    = errors.New("ontology: pin counter already zero")
	ErrAllPinned       = errors.New("ontology: all pages pinned")
)

const (
	maxPage = 1_000_000
	maxNow  = 1_000_000_000_000_000
)

type segment uint8

const (
	segYoung segment = iota
	segOld
)

type entry struct {
	page  int
	first int64
	hasF  bool
	pin   int
	seg   segment
	el    *list.Element
}

// Pool is a thread-safe two-segment LRU buffer pool. The global LRU order is
// the head-to-tail of the young list followed by the head-to-tail of the old
// list. The head of young is the most-recently-used end; the tail of old is
// the eviction end.
type Pool struct {
	mu  sync.Mutex
	n   int
	rho int
	tol int
	t   int64

	young *list.List
	old   *list.List
	index map[int]*entry

	now       int64
	evictions []int
}

// New creates a Pool of capacity n with old-segment target percentage rho,
// rebalance tolerance tol and promotion residence time t.
func New(n int, rho int, tol int, t int64) (*Pool, error) {
	if n < 4 || n > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	if rho < 5 || rho > 95 {
		return nil, ErrInvalidArgument
	}
	if tol < 0 || tol > n {
		return nil, ErrInvalidArgument
	}
	if t < 0 || t > 1_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &Pool{
		n:     n,
		rho:   rho,
		tol:   tol,
		t:     t,
		young: list.New(),
		old:   list.New(),
		index: make(map[int]*entry),
	}, nil
}

// AccessResult is the outcome of Access or Prefetch.
type AccessResult struct {
	Hit     bool
	Evicted bool
	Page    int
}

// rebalance restores tgt <= len(old) <= tgt+tol for tgt=floor(len*rho/100).
// It moves the young tail to the old head when old is too small, and moves
// the old head to the young tail when old is too large. Callers must hold mu.
func (p *Pool) rebalance() {
	tgt := (len(p.index) * p.rho) / 100
	for p.old.Len() < tgt && p.young.Len() > 0 {
		p.moveToOldHead(p.young.Back())
	}
	for p.old.Len() > tgt+p.tol {
		p.moveToYoungTail(p.old.Front())
	}
}

func (p *Pool) moveToOldHead(el *list.Element) {
	e := el.Value.(*entry)
	if e.seg == segYoung {
		p.young.Remove(el)
	} else {
		p.old.Remove(el)
	}
	e.seg = segOld
	e.el = p.old.PushFront(e)
}

func (p *Pool) moveToYoungHead(el *list.Element) {
	e := el.Value.(*entry)
	if e.seg == segYoung {
		p.young.Remove(el)
	} else {
		p.old.Remove(el)
	}
	e.seg = segYoung
	e.el = p.young.PushFront(e)
}

func (p *Pool) moveToYoungTail(el *list.Element) {
	e := el.Value.(*entry)
	if e.seg == segYoung {
		p.young.Remove(el)
	} else {
		p.old.Remove(el)
	}
	e.seg = segYoung
	e.el = p.young.PushBack(e)
}

// chooseVictim scans the old list from tail toward head for the first
// unpinned page, then the young list tail toward head. Callers hold mu.
func (p *Pool) chooseVictim() *entry {
	for el := p.old.Back(); el != nil; el = el.Prev() {
		if e := el.Value.(*entry); e.pin == 0 {
			return e
		}
	}
	for el := p.young.Back(); el != nil; el = el.Prev() {
		if e := el.Value.(*entry); e.pin == 0 {
			return e
		}
	}
	return nil
}

func (p *Pool) evict(e *entry) {
	if e.seg == segOld {
		p.old.Remove(e.el)
	} else {
		p.young.Remove(e.el)
	}
	delete(p.index, e.page)
	p.evictions = append(p.evictions, e.page)
}

// miss handles a page fault: evict a victim when full, insert the new page at
// the head of old (first set to now iff setFirst), then rebalance once.
func (p *Pool) miss(page int, now int64, setFirst bool) (AccessResult, error) {
	res := AccessResult{Hit: false}
	if len(p.index) >= p.n {
		victim := p.chooseVictim()
		if victim == nil {
			return AccessResult{}, ErrAllPinned
		}
		res.Evicted = true
		res.Page = victim.page
		p.evict(victim)
	}
	e := &entry{page: page, first: now, hasF: setFirst}
	e.seg = segOld
	e.el = p.old.PushFront(e)
	p.index[page] = e
	p.rebalance()
	return res, nil
}

// Access reads or touches a page at time now.
func (p *Pool) Access(page int, now int64) (AccessResult, error) {
	if page < 0 || page > maxPage || now < 0 || now > maxNow {
		return AccessResult{}, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return AccessResult{}, ErrClockSkew
	}
	e, ok := p.index[page]
	if !ok {
		// A full, fully-pinned pool rejects the fault: validate the victim
		// before advancing the clock so rejection changes nothing.
		if len(p.index) >= p.n && p.chooseVictim() == nil {
			return AccessResult{}, ErrAllPinned
		}
		p.now = now
		return p.miss(page, now, true)
	}
	p.now = now
	switch e.seg {
	case segYoung:
		p.moveToYoungHead(e.el)
	case segOld:
		if !e.hasF {
			e.first = now
			e.hasF = true
			break
		}
		if now-e.first >= p.t {
			p.moveToYoungHead(e.el)
			p.rebalance()
		}
	}
	return AccessResult{Hit: true}, nil
}

// Prefetch loads a page at time now without recording a first-access time.
// A prefetch of a page already in the pool only advances the clock.
func (p *Pool) Prefetch(page int, now int64) (AccessResult, error) {
	if page < 0 || page > maxPage || now < 0 || now > maxNow {
		return AccessResult{}, ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return AccessResult{}, ErrClockSkew
	}
	if _, ok := p.index[page]; ok {
		p.now = now
		return AccessResult{Hit: true}, nil
	}
	if len(p.index) >= p.n && p.chooseVictim() == nil {
		return AccessResult{}, ErrAllPinned
	}
	p.now = now
	return p.miss(page, now, false)
}

// Pin increments the pin counter of a page. It neither advances nor checks
// the clock.
func (p *Pool) Pin(page int) error {
	if page < 0 || page > maxPage {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.index[page]
	if !ok {
		return ErrNotFound
	}
	e.pin++
	return nil
}

// Unpin decrements the pin counter of a page.
func (p *Pool) Unpin(page int) error {
	if page < 0 || page > maxPage {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.index[page]
	if !ok {
		return ErrNotFound
	}
	if e.pin == 0 {
		return ErrPinUnderflow
	}
	e.pin--
	return nil
}

func snapshot(l *list.List) []int {
	out := make([]int, 0, l.Len())
	for el := l.Front(); el != nil; el = el.Next() {
		out = append(out, el.Value.(*entry).page)
	}
	return out
}

// Lists returns the young and old page lists, each ordered head to tail.
func (p *Pool) Lists() (young []int, old []int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return snapshot(p.young), snapshot(p.old)
}

// Evictions returns a copy of the pages evicted so far, in eviction order.
func (p *Pool) Evictions() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]int, len(p.evictions))
	copy(out, p.evictions)
	return out
}

// Len returns the number of pages currently held in the pool.
func (p *Pool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.index)
}
