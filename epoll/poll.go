package epoll

import (
	"sort"
	"sync"
)

const (
	IN      = 1
	OUT     = 2
	ERR     = 4
	HUP     = 8
	ET      = 1
	ONESHOT = 2
	EXCL    = 4
)

var (
	ErrInvalid    = invalidError{}
	ErrExists     = existsError{}
	ErrNoSpace    = noSpaceError{}
	ErrTooMany    = tooManyError{}
	ErrNoEnt      = noEntError{}
	ErrExclChange = exclChangeError{}
)

type invalidError struct{}

func (invalidError) Error() string { return "invalid argument" }

type existsError struct{}

func (existsError) Error() string { return "watch already exists" }

type noSpaceError struct{}

func (noSpaceError) Error() string { return "instance watch limit reached" }

type tooManyError struct{}

func (tooManyError) Error() string { return "global watch limit reached" }

type noEntError struct{}

func (noEntError) Error() string { return "watch does not exist" }

type exclChangeError struct{}

func (exclChangeError) Error() string { return "exclusive flag cannot change" }

type Event struct {
	FD      int
	Revents int
}

type Watch struct {
	FD       int
	Interest int
	Flags    int
	Eff      int
	Queued   bool
	Disabled bool
}

type watch struct {
	ep       int
	fd       int
	interest int
	flags    int
	eff      int
	queued   bool
	disabled bool
	prev     *watch
	next     *watch
}

type instance struct {
	watches map[int]*watch
	head    *watch
	tail    *watch
	count   int
}

type Poll struct {
	mu        sync.RWMutex
	wLimit    int
	instances []*instance
	uLimit    int
	states    map[int]int
	byFile    map[int][]*watch
	total     int
	examined  int
}

func New(wLimit, instances, globalLimit int) (*Poll, error) {
	if wLimit < 1 || wLimit > 1_000_000 || instances < 1 || instances > 4 || globalLimit < 1 || globalLimit > 1_000_000 {
		return nil, ErrInvalid
	}

	p := &Poll{
		wLimit:    wLimit,
		instances: make([]*instance, instances),
		uLimit:    globalLimit,
		states:    make(map[int]int),
		byFile:    make(map[int][]*watch),
	}
	for ep := range p.instances {
		p.instances[ep] = &instance{watches: make(map[int]*watch)}
	}
	return p, nil
}

func validInterestFlags(interest, flags int) bool {
	return interest >= 0 && interest <= 15 &&
		flags&^7 == 0 &&
		(flags&(EXCL|ONESHOT) != EXCL|ONESHOT) &&
		(flags&EXCL == 0 || interest&(ERR|HUP) == 0)
}

func (p *Poll) validEP(ep int) bool {
	return ep >= 0 && ep < len(p.instances)
}

func effectiveInterest(interest int) int {
	return interest | ERR | HUP
}

func (in *instance) appendQueue(w *watch) {
	w.queued = true
	w.prev = in.tail
	w.next = nil
	if in.tail != nil {
		in.tail.next = w
	} else {
		in.head = w
	}
	in.tail = w
}

func (in *instance) popHead() *watch {
	w := in.head
	if w == nil {
		return nil
	}

	in.head = w.next
	if in.head != nil {
		in.head.prev = nil
	} else {
		in.tail = nil
	}
	w.prev = nil
	w.next = nil
	w.queued = false
	return w
}

func (in *instance) removeQueue(w *watch) {
	if !w.queued {
		return
	}

	if w.prev != nil {
		w.prev.next = w.next
	} else {
		in.head = w.next
	}
	if w.next != nil {
		w.next.prev = w.prev
	} else {
		in.tail = w.prev
	}
	w.prev = nil
	w.next = nil
	w.queued = false
}

func (p *Poll) Add(ep, fd, interest, flags int) error {
	if !p.validEP(ep) || fd < 0 || !validInterestFlags(interest, flags) {
		return ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	in := p.instances[ep]
	if _, ok := in.watches[fd]; ok {
		return ErrExists
	}
	if in.count >= p.wLimit {
		return ErrNoSpace
	}
	if p.total >= p.uLimit {
		return ErrTooMany
	}

	w := &watch{
		ep:       ep,
		fd:       fd,
		interest: interest,
		flags:    flags,
		eff:      effectiveInterest(interest),
	}
	in.watches[fd] = w
	in.count++
	p.total++
	if len(p.byFile[fd]) == 0 {
		p.byFile[fd] = make([]*watch, len(p.instances))
	}
	p.byFile[fd][ep] = w

	if p.states[fd]&w.eff != 0 {
		in.appendQueue(w)
	}
	return nil
}

func (p *Poll) Mod(ep, fd, interest, flags int) error {
	if !p.validEP(ep) || fd < 0 || !validInterestFlags(interest, flags) {
		return ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	in := p.instances[ep]
	w, ok := in.watches[fd]
	if !ok {
		return ErrNoEnt
	}
	if w.flags&EXCL != flags&EXCL {
		return ErrExclChange
	}

	w.interest = interest
	w.flags = flags
	w.eff = effectiveInterest(interest)
	w.disabled = false
	if !w.queued && p.states[fd]&w.eff != 0 {
		in.appendQueue(w)
	}
	return nil
}

func (p *Poll) Del(ep, fd int) error {
	if !p.validEP(ep) || fd < 0 {
		return ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	in := p.instances[ep]
	w, ok := in.watches[fd]
	if !ok {
		return ErrNoEnt
	}

	in.removeQueue(w)
	delete(in.watches, fd)
	in.count--
	p.total--
	p.byFile[fd][ep] = nil
	return nil
}

func (p *Poll) Close(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	watchers := p.byFile[fd]
	removed := 0
	for ep, w := range watchers {
		if w == nil {
			continue
		}
		p.instances[ep].removeQueue(w)
		delete(p.instances[ep].watches, fd)
		p.instances[ep].count--
		watchers[ep] = nil
		removed++
	}
	p.total -= removed
	p.states[fd] = 0
	return removed, nil
}

func (p *Poll) SetState(fd, state int) error {
	if fd < 0 || state < 0 || state > 15 {
		return ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	old := p.states[fd]
	p.states[fd] = state
	watchers := p.byFile[fd]
	var exclusive *watch

	for ep, w := range watchers {
		if w == nil || w.disabled || w.queued {
			continue
		}

		ready := false
		if w.flags&ET != 0 {
			ready = state&^old&w.eff != 0
		} else {
			ready = state&w.eff != 0
		}
		if !ready {
			continue
		}

		if w.flags&EXCL != 0 {
			if exclusive == nil || ep < exclusive.ep {
				exclusive = w
			}
			continue
		}
		p.instances[ep].appendQueue(w)
	}
	if exclusive != nil {
		p.instances[exclusive.ep].appendQueue(exclusive)
	}
	return nil
}

func (p *Poll) Wait(ep, max int) ([]Event, error) {
	if !p.validEP(ep) || max < 1 || max > 1_000_000 {
		return nil, ErrInvalid
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	in := p.instances[ep]
	events := make([]Event, 0)
	refill := make([]*watch, 0)

	for len(events) < max && in.head != nil {
		w := in.popHead()
		p.examined++
		revents := p.states[w.fd] & w.eff
		if revents == 0 {
			continue
		}

		events = append(events, Event{FD: w.fd, Revents: revents})
		if w.flags&ONESHOT != 0 {
			w.disabled = true
		} else if w.flags&ET == 0 {
			refill = append(refill, w)
		}
	}

	for _, w := range refill {
		in.appendQueue(w)
	}
	return events, nil
}

func (p *Poll) State(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.states[fd], nil
}

func (p *Poll) Queue(ep int) ([]int, error) {
	if !p.validEP(ep) {
		return nil, ErrInvalid
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	in := p.instances[ep]
	queue := make([]int, 0, in.count)
	for w := in.head; w != nil; w = w.next {
		queue = append(queue, w.fd)
	}
	return queue, nil
}

func (p *Poll) Watches(ep int) ([]Watch, error) {
	if !p.validEP(ep) {
		return nil, ErrInvalid
	}

	p.mu.RLock()
	defer p.mu.RUnlock()

	in := p.instances[ep]
	fds := make([]int, 0, len(in.watches))
	for fd := range in.watches {
		fds = append(fds, fd)
	}
	sort.Ints(fds)

	watches := make([]Watch, 0, len(fds))
	for _, fd := range fds {
		w := in.watches[fd]
		watches = append(watches, Watch{
			FD:       w.fd,
			Interest: w.interest,
			Flags:    w.flags,
			Eff:      w.eff,
			Queued:   w.queued,
			Disabled: w.disabled,
		})
	}
	return watches, nil
}
