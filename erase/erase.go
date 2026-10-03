package erase

import (
	"errors"
	"sort"
	"sync"

	"ontology/hold"
	"ontology/restore"
)

var (
	ErrInvalid   = errors.New("erase: invalid argument")
	ErrRole      = errors.New("erase: insufficient role")
	ErrClock     = errors.New("erase: clock moved backwards")
	ErrDuplicate = errors.New("erase: duplicate active request")
	ErrAlready   = hold.ErrAlready
	ErrNotHeld   = hold.ErrNotHeld
	ErrState     = restore.ErrState
	ErrNoBackup  = restore.ErrNoBackup
	ErrRestoring = restore.ErrRestoring
)

type Status int

const (
	Active Status = iota + 1
	Deferred
	Done
)

type OverdueEntry struct {
	Erasure        int
	PendingSystems []int
}

type Erasure struct {
	ID          int
	Subject     int
	Status      Status
	RequestedAt int
	Deadline    int
	Acks        map[int]int
	pending     map[int]bool
}

type Ledger struct {
	mu               sync.Mutex
	systems          int
	sla              int
	clock            int
	nextErasure      int
	erasures         []*Erasure
	openBySubject    map[int]int
	activeByDeadline *deadlineHeap
	holds            *hold.Manager
	restores         *restore.Manager
}

func New(systems, sla int) *Ledger {
	if systems < 1 || systems > 8 || sla < 1 || sla > 1_000_000_000 {
		panic("erase: invalid construction parameters")
	}
	l := &Ledger{
		systems:       systems,
		sla:           sla,
		holds:         hold.NewManager(systems),
		restores:      restore.NewManager(systems),
		openBySubject: make(map[int]int),
	}
	l.activeByDeadline = newDeadlineHeap()
	return l
}

func (l *Ledger) Request(role, subject, now int) (int, error) {
	if role < 1 || role > 3 || subject < 1 || subject > 1_000_000 || now < 0 {
		return 0, ErrInvalid
	}
	if role != 1 {
		return 0, ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return 0, ErrClock
	}
	if _, open := l.openBySubject[subject]; open {
		return 0, ErrDuplicate
	}
	l.clock = now
	l.nextErasure++
	erasure := &Erasure{
		ID:          l.nextErasure,
		Subject:     subject,
		RequestedAt: now,
		Acks:        make(map[int]int),
		pending:     make(map[int]bool, l.systems),
	}
	for system := 1; system <= l.systems; system++ {
		erasure.pending[system] = true
	}
	l.erasures = append(l.erasures, erasure)
	if l.holds.IsHeld(subject) {
		erasure.Status = Deferred
	} else {
		erasure.Status = Active
		erasure.Deadline = now + l.sla
		l.activeByDeadline.push(erasure.ID, erasure.Deadline)
	}
	l.openBySubject[subject] = erasure.ID
	return erasure.ID, nil
}

func (l *Ledger) Hold(role, subject, now int) error {
	if role < 1 || role > 3 || subject < 1 || subject > 1_000_000 || now < 0 {
		return ErrInvalid
	}
	if role != 2 {
		return ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return ErrClock
	}
	if err := l.holds.Hold(role, subject, now); err != nil {
		return err
	}
	l.clock = now
	return nil
}

func (l *Ledger) Release(role, subject, now int) error {
	if role < 1 || role > 3 || subject < 1 || subject > 1_000_000 || now < 0 {
		return ErrInvalid
	}
	if role != 2 {
		return ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return ErrClock
	}
	if err := l.holds.Release(role, subject, now); err != nil {
		return err
	}
	l.clock = now
	if id := l.openBySubject[subject]; id != 0 {
		erasure := l.erasures[id-1]
		if erasure.Status == Deferred {
			erasure.Status = Active
			erasure.Deadline = now + l.sla
			l.activeByDeadline.push(id, erasure.Deadline)
		}
	}
	return nil
}

func (l *Ledger) Ack(role, erasure, system, now int) error {
	if role < 1 || role > 3 || erasure < 1 || erasure > l.nextErasure || system < 1 || system > l.systems || now < 0 {
		return ErrInvalid
	}
	if role != 3 {
		return ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return ErrClock
	}
	target := l.erasures[erasure-1]
	if target.Status != Active || !target.pending[system] {
		return ErrState
	}
	l.clock = now
	target.Acks[system] = now
	delete(target.pending, system)
	l.restores.RecordAck(erasure, system, now)
	if len(target.pending) == 0 {
		target.Status = Done
		l.activeByDeadline.remove(erasure)
		delete(l.openBySubject, target.Subject)
	}
	return nil
}

func (l *Ledger) Overdue(now int) []OverdueEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make([]OverdueEntry, 0)
	reported := make([]int, 0)
	for l.activeByDeadline.len() > 0 {
		key := l.activeByDeadline.peek()
		target := l.erasures[key.id-1]
		if key.deadline > now {
			break
		}
		l.activeByDeadline.pop()
		id := key.id
		pending := make([]int, 0, len(target.pending))
		for system := range target.pending {
			pending = append(pending, system)
		}
		sort.Ints(pending)
		reported = append(reported, id)
		result = append(result, OverdueEntry{Erasure: id, PendingSystems: pending})
	}
	for _, id := range reported {
		l.activeByDeadline.push(id, l.erasures[id-1].Deadline)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Erasure < result[j].Erasure
	})
	return result
}

func (l *Ledger) Backup(role, system, now int) (int, error) {
	if role < 1 || role > 3 || system < 1 || system > l.systems || now < 0 {
		return 0, ErrInvalid
	}
	if role != 3 {
		return 0, ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return 0, ErrClock
	}
	id, err := l.restores.Backup(role, system, now)
	if err != nil {
		return 0, err
	}
	l.clock = now
	return id, nil
}

func (l *Ledger) Restore(role, system, backup, now int) ([]int, error) {
	if role < 1 || role > 3 || system < 1 || system > l.systems || backup < 1 || now < 0 {
		return nil, ErrInvalid
	}
	if role != 3 {
		return nil, ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return nil, ErrClock
	}
	replay, err := l.restores.Restore(role, system, backup, now)
	if err != nil {
		return nil, err
	}
	l.clock = now
	return replay, nil
}

func (l *Ledger) ReapplyDone(role, system, erasure, now int) error {
	if role < 1 || role > 3 || system < 1 || system > l.systems || erasure < 1 || erasure > l.nextErasure || now < 0 {
		return ErrInvalid
	}
	if role != 3 {
		return ErrRole
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if now < l.clock {
		return ErrClock
	}
	if err := l.restores.ReapplyDone(role, system, erasure, now); err != nil {
		return err
	}
	l.clock = now
	return nil
}

func (l *Ledger) Read(system int) error {
	if system < 1 || system > l.systems {
		return ErrInvalid
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.restores.Read(system)
}

func (l *Ledger) Erasure(id int) (Erasure, bool) {
	if id < 1 || id > l.nextErasure {
		return Erasure{}, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	target := l.erasures[id-1]
	copyValue := *target
	copyValue.Acks = make(map[int]int, len(target.Acks))
	for system, ackedAt := range target.Acks {
		copyValue.Acks[system] = ackedAt
	}
	return copyValue, true
}

func (l *Ledger) overdueExaminedForTest(now int) ([]OverdueEntry, int) {
	l.activeByDeadline.resetExamined()
	entries := l.Overdue(now)
	return entries, l.activeByDeadline.examinedCount()
}
