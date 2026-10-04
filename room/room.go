package room

import (
	"errors"
	"sync"

	"ontology/queue"
	"ontology/triage"
)

var (
	ErrInvalid  = errors.New("room: invalid argument")
	ErrClock    = errors.New("room: clock moved backwards")
	ErrNotFound = errors.New("room: patient or room not found")
	ErrState    = errors.New("room: illegal state")
	ErrNoCallee = errors.New("room: no eligible patient to call")
)

type Status = queue.Status

const (
	Waiting  = queue.Waiting
	Called   = queue.Called
	Gone     = queue.Gone
	Finished = queue.Finished
)

type Kind int

const (
	Rescue Kind = iota
	Clinic
)

type RoomID string

type CallResult struct {
	Patient string
	Level   int
	Skipped []string
}

type roomInfo struct {
	kind     Kind
	occupant *queue.Entry
	arrived  bool
}

// called 键为 ((callAt+A)<<32 | regNo)，值携带 callAt。
type calledInfo struct {
	e      *queue.Entry
	room   *roomInfo
	callAt int
}

type System struct {
	mu     sync.Mutex
	qq     *queue.Q
	rooms  map[queue.ID]*roomInfo
	called *queue.Treap[calledInfo]
	maxNow int
	// landExamined 统计落地时从已叫号 treap 取出的人数。
	landExamined int
	landedMiss   int
}

func NewSystem(r1, r2, r3, r4, a int) *System {
	return &System{
		qq:     queue.New(r1, r2, r3, r4, a),
		rooms:  make(map[queue.ID]*roomInfo),
		called: queue.NewTreap[calledInfo](),
	}
}

func (s *System) AddRoom(room RoomID, kind Kind) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if room == "" || (kind != Rescue && kind != Clinic) {
		return ErrInvalid
	}
	if _, ok := s.rooms[queue.ID(room)]; ok {
		return ErrNotFound
	}
	s.rooms[queue.ID(room)] = &roomInfo{kind: kind}
	return nil
}

func (s *System) Register(now int, patient string, v triage.Vitals) (regNo, level int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validClock(now) || patient == "" || !triage.ValidVitals(v) {
		return 0, 0, ErrInvalid
	}
	if now < s.maxNow {
		return 0, 0, ErrClock
	}
	if _, ok := s.qq.Get(queue.ID(patient)); ok {
		return 0, 0, ErrNotFound
	}
	s.land(now)
	s.maxNow = now
	e := s.qq.Register(now, queue.ID(patient), v)
	return e.RegNo, e.Level, nil
}

func (s *System) Reassess(now int, patient string, v triage.Vitals) (level int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validClock(now) || patient == "" || !triage.ValidVitals(v) {
		return 0, ErrInvalid
	}
	if now < s.maxNow {
		return 0, ErrClock
	}
	e, ok := s.qq.Get(queue.ID(patient))
	if !ok {
		return 0, ErrNotFound
	}
	if e.Status != queue.Waiting {
		return 0, ErrState
	}
	s.land(now)
	s.maxNow = now
	e, _ = s.qq.Reassess(now, queue.ID(patient), v)
	return e.Level, nil
}

func (s *System) Call(now int, room RoomID) (CallResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validClock(now) || room == "" {
		return CallResult{}, ErrInvalid
	}
	if now < s.maxNow {
		return CallResult{}, ErrClock
	}
	rm, ok := s.rooms[queue.ID(room)]
	if !ok {
		return CallResult{}, ErrNotFound
	}
	if rm.occupant != nil {
		if rm.arrived || rm.occupant.CallAt+s.grace() >= now {
			return CallResult{}, ErrState
		}
	}
	s.land(now)
	s.maxNow = now

	levels := s.callLevels(rm.kind)
	var skipped []string
	var picked *queue.Entry
	picked = s.qq.FirstEligible(now, levels, func(e *queue.Entry) bool {
		if s.qq.Overdue(now, e) {
			skipped = append(skipped, string(e.Patient))
			return false
		}
		return true
	})
	if picked == nil {
		return CallResult{}, ErrNoCallee
	}
	s.qq.MarkCalled(picked)
	rm.occupant = picked
	picked.CallAt = now
	picked.Room = rm
	key := s.calledKey(picked, now)
	s.called.Insert(key, calledInfo{e: picked, room: rm, callAt: now})
	if skipped == nil {
		skipped = []string{}
	}
	return CallResult{Patient: string(picked.Patient), Level: picked.Level, Skipped: skipped}, nil
}

func (s *System) Arrive(now int, patient string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validClock(now) || patient == "" {
		return ErrInvalid
	}
	if now < s.maxNow {
		return ErrClock
	}
	e, ok := s.qq.Get(queue.ID(patient))
	if !ok {
		return ErrNotFound
	}
	if e.Status != queue.Called || e.Arrived {
		return ErrState
	}
	if e.CallAt+s.grace() < now {
		return ErrState
	}
	s.land(now)
	s.maxNow = now
	if e.Status != queue.Called {
		return ErrState
	}
	s.removeCalled(e)
	s.qq.MarkArrived(e)
	e.Room.(*roomInfo).arrived = true
	return nil
}

func (s *System) calledKey(e *queue.Entry, callAt int) uint64 {
	return uint64(callAt+s.grace())<<32 | uint64(e.RegNo)
}

func (s *System) removeCalled(e *queue.Entry) {
	s.called.Delete(s.calledKey(e, e.CallAt))
}

func (s *System) Finish(now int, room RoomID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validClock(now) || room == "" {
		return "", ErrInvalid
	}
	if now < s.maxNow {
		return "", ErrClock
	}
	rm, ok := s.rooms[queue.ID(room)]
	if !ok {
		return "", ErrNotFound
	}
	if rm.occupant == nil {
		return "", ErrState
	}
	if !rm.arrived {
		return "", ErrState
	}
	s.land(now)
	s.maxNow = now
	if rm.occupant == nil || !rm.arrived {
		return "", ErrState
	}
	e := rm.occupant
	patient := string(e.Patient)
	s.qq.Finish(e)
	rm.occupant = nil
	rm.arrived = false
	return patient, nil
}

func validClock(now int) bool {
	return now >= 0 && now <= 1_000_000_000
}

func (s *System) grace() int {
	return s.qq.Grace()
}

func (s *System) callLevels(k Kind) []int {
	if k == Rescue {
		return []int{1, 2}
	}
	return []int{2, 3, 4}
}

// land 落地全部 now 时刻已过号（deadline < now）的已叫号者。
// 被取出者要么实际过号、要么恰为首个未过号者，故取出数 <= 实际过号数+1。
func (s *System) land(now int) {
	for {
		key, info, ok := s.called.PopFirst()
		if !ok {
			return
		}
		s.landExamined++
		deadline := int(key >> 32)
		if deadline >= now {
			s.called.Insert(key, info)
			return
		}
		s.landedMiss++
		info.room.occupant = nil
		info.room.arrived = false
		s.qq.Return(info.e, deadline)
	}
}
