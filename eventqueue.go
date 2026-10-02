package ontology

import (
	"errors"
	"sort"
	"sync"
)

const (
	In      = 1
	Out     = 2
	Err     = 4
	Hup     = 8
	ET      = 1
	OneShot = 2
	Excl    = 4
)

var (
	ErrInvalid    = errors.New("invalid argument")
	ErrExists     = errors.New("watch exists")
	ErrNoSpace    = errors.New("instance watch limit reached")
	ErrTooMany    = errors.New("global watch limit reached")
	ErrNoEnt      = errors.New("watch does not exist")
	ErrExclChange = errors.New("exclusive flag cannot change")
)

type Event struct {
	Fd      int
	Revents int
}

type Watch struct {
	Fd       int
	Interest int
	Flags    int
	Eff      int
	Queued   bool
	Disabled bool
}

type watch struct {
	Watch
	ep    int
	index int
	prev  *watch
	next  *watch
}

type fileState struct {
	state   int
	watches []*watch
}

type readyList struct {
	head *watch
	tail *watch
}

type EventQueue struct {
	mu          sync.Mutex
	perInst     int
	instances   int
	global      int
	globalCount int
	files       map[int]*fileState
	tables      []map[int]*watch
	queues      []readyList
	examined    int
}

func NewEventQueue(w, e, u int) (*EventQueue, error) {
	if w < 1 || w > 1_000_000 || e < 1 || e > 4 || u < 1 || u > 1_000_000 {
		return nil, ErrInvalid
	}

	q := &EventQueue{
		perInst:   w,
		instances: e,
		global:    u,
		files:     make(map[int]*fileState),
		tables:    make([]map[int]*watch, e),
		queues:    make([]readyList, e),
	}
	for ep := range q.tables {
		q.tables[ep] = make(map[int]*watch)
	}
	return q, nil
}

func (q *EventQueue) Add(ep, fd, interest, flags int) error {
	if !q.validInstance(ep) || fd < 0 || !validMask(interest) || !validFlags(flags) {
		return ErrInvalid
	}
	if flags&Excl != 0 && (flags&OneShot != 0 || interest&(Err|Hup) != 0) {
		return ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	table := q.tables[ep]
	if _, ok := table[fd]; ok {
		return ErrExists
	}
	if len(table) >= q.perInst {
		return ErrNoSpace
	}
	if q.globalCount >= q.global {
		return ErrTooMany
	}

	fs := q.files[fd]
	if fs == nil {
		fs = &fileState{}
		q.files[fd] = fs
	}

	entry := &watch{
		Watch: Watch{
			Fd:       fd,
			Interest: interest,
			Flags:    flags,
			Eff:      effectiveInterest(interest),
		},
		ep:    ep,
		index: len(fs.watches),
	}
	table[fd] = entry
	q.globalCount++
	fs.watches = append(fs.watches, entry)

	if fs.state&entry.Eff != 0 {
		q.enqueueLocked(entry)
	}
	return nil
}

func (q *EventQueue) Mod(ep, fd, interest, flags int) error {
	if !q.validInstance(ep) || fd < 0 || !validMask(interest) || !validFlags(flags) {
		return ErrInvalid
	}
	if flags&Excl != 0 && (flags&OneShot != 0 || interest&(Err|Hup) != 0) {
		return ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	entry := q.tables[ep][fd]
	if entry == nil {
		return ErrNoEnt
	}
	if entry.Flags&Excl != flags&Excl {
		return ErrExclChange
	}

	entry.Interest = interest
	entry.Flags = flags
	entry.Eff = effectiveInterest(interest)
	entry.Disabled = false
	if !entry.Queued && q.files[fd].state&entry.Eff != 0 {
		q.enqueueLocked(entry)
	}
	return nil
}

func (q *EventQueue) Del(ep, fd int) error {
	if !q.validInstance(ep) || fd < 0 {
		return ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	entry := q.tables[ep][fd]
	if entry == nil {
		return ErrNoEnt
	}
	q.removeWatchLocked(ep, fd, entry)
	return nil
}

func (q *EventQueue) Close(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	fs := q.files[fd]
	if fs == nil {
		fs = &fileState{}
		q.files[fd] = fs
	}

	removed := len(fs.watches)
	for _, entry := range fs.watches {
		q.unlinkLocked(entry)
		delete(q.tables[entry.ep], fd)
		q.globalCount--
	}
	fs.watches = nil
	fs.state = 0
	return removed, nil
}

func (q *EventQueue) SetState(fd, state int) error {
	if fd < 0 || !validMask(state) {
		return ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	fs := q.files[fd]
	if fs == nil {
		fs = &fileState{}
		q.files[fd] = fs
	}

	old := fs.state
	fs.state = state

	var exclusiveCandidate *watch
	fileWatches := append([]*watch(nil), fs.watches...)
	sort.Slice(fileWatches, func(i, j int) bool {
		return fileWatches[i].ep < fileWatches[j].ep
	})
	for _, entry := range fileWatches {
		q.examined++
		triggered := false
		if !entry.Disabled && !entry.Queued {
			if entry.Flags&ET == 0 {
				triggered = state&entry.Eff != 0
			} else {
				triggered = state&^old&entry.Eff != 0
			}
		}
		if !triggered {
			continue
		}

		if entry.Flags&Excl == 0 {
			q.enqueueLocked(entry)
		} else if exclusiveCandidate == nil || entry.ep < exclusiveCandidate.ep {
			exclusiveCandidate = entry
		}
	}
	if exclusiveCandidate != nil {
		q.enqueueLocked(exclusiveCandidate)
	}
	return nil
}

func (q *EventQueue) Wait(ep, max int) ([]Event, error) {
	if !q.validInstance(ep) || max < 1 || max > 1_000_000 {
		return nil, ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	events := make([]Event, 0)
	ready := make([]*watch, 0)
	list := &q.queues[ep]

	for len(events) < max && list.head != nil {
		entry := list.head
		q.examined++
		q.unlinkLocked(entry)

		revents := q.files[entry.Fd].state & entry.Eff
		if revents == 0 {
			continue
		}

		events = append(events, Event{Fd: entry.Fd, Revents: revents})
		if entry.Flags&OneShot != 0 {
			entry.Disabled = true
			continue
		}
		if entry.Flags&ET == 0 {
			ready = append(ready, entry)
		}
	}

	for _, entry := range ready {
		q.enqueueLocked(entry)
	}
	return events, nil
}

func (q *EventQueue) State(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if fs := q.files[fd]; fs != nil {
		return fs.state, nil
	}
	return 0, nil
}

func (q *EventQueue) Queue(ep int) ([]int, error) {
	if !q.validInstance(ep) {
		return nil, ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	fds := make([]int, 0)
	for entry := q.queues[ep].head; entry != nil; entry = entry.next {
		fds = append(fds, entry.Fd)
	}
	return fds, nil
}

func (q *EventQueue) Watches(ep int) ([]Watch, error) {
	if !q.validInstance(ep) {
		return nil, ErrInvalid
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	table := q.tables[ep]
	watches := make([]Watch, 0, len(table))
	for _, entry := range table {
		watches = append(watches, entry.Watch)
	}
	sort.Slice(watches, func(i, j int) bool {
		return watches[i].Fd < watches[j].Fd
	})
	return watches, nil
}

func (q *EventQueue) validInstance(ep int) bool {
	return ep >= 0 && ep < q.instances
}

func (q *EventQueue) enqueueLocked(entry *watch) {
	if entry.Queued {
		return
	}

	list := &q.queues[entry.ep]
	entry.prev = list.tail
	entry.next = nil
	if list.tail != nil {
		list.tail.next = entry
	} else {
		list.head = entry
	}
	list.tail = entry
	entry.Queued = true
}

func (q *EventQueue) unlinkLocked(entry *watch) {
	if !entry.Queued {
		return
	}

	list := &q.queues[entry.ep]
	if list.head == entry {
		list.head = entry.next
	}
	if list.tail == entry {
		list.tail = entry.prev
	}
	if entry.prev != nil {
		entry.prev.next = entry.next
	}
	if entry.next != nil {
		entry.next.prev = entry.prev
	}
	entry.prev = nil
	entry.next = nil
	entry.Queued = false
}

func (q *EventQueue) removeWatchLocked(ep, fd int, entry *watch) {
	q.unlinkLocked(entry)
	delete(q.tables[ep], fd)
	q.globalCount--

	fs := q.files[fd]
	last := fs.watches[len(fs.watches)-1]
	fs.watches[entry.index] = last
	last.index = entry.index
	fs.watches[len(fs.watches)-1] = nil
	fs.watches = fs.watches[:len(fs.watches)-1]
	if len(fs.watches) == 0 {
		fs.watches = nil
	}
}

func effectiveInterest(interest int) int {
	return interest | Err | Hup
}

func validMask(value int) bool {
	return value >= 0 && value <= 15
}

func validFlags(flags int) bool {
	return flags >= 0 && flags&^(ET|OneShot|Excl) == 0
}
