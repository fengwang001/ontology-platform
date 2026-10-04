package schedule

import (
	"errors"
	"sort"
	"sync"

	"ontology/equip"
	"ontology/room"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockRolledBack = errors.New("operation time is before accepted time")
	ErrIDExists        = errors.New("surgery id already exists")
	ErrUnknownRoom     = errors.New("room does not exist")
	ErrUnknownEquip    = errors.New("equipment type does not exist")
	ErrExists          = errors.New("resource already exists")
	ErrRoomConflict    = errors.New("room conflict")
	ErrSurgeonConflict = errors.New("surgeon conflict")
	ErrEquipConflict   = errors.New("equipment conflict")
	ErrNoRoom          = errors.New("no room is registered")
	ErrBadState        = errors.New("surgery cannot be cancelled")
)

type TypeError struct {
	Err  error
	Type string
}

func (e TypeError) Error() string {
	if e.Type == "" {
		return e.Err.Error()
	}
	return e.Err.Error() + ": " + e.Type
}

func (e TypeError) Unwrap() error { return e.Err }

type Needs map[string]int

type Surgery struct {
	ID        string
	Room      string
	Start     int64
	Dur       int64
	End       int64
	Surgeon   string
	Needs     Needs
	Emergency bool
}

type EmergencyResult struct {
	Room      string
	Start     int64
	Displaced []Surgery
}

type Scheduler struct {
	mu           sync.Mutex
	rooms        *room.Registry
	equips       *equip.Pool
	now          int64
	haveOp       bool
	byID         map[string]Surgery
	list         []Surgery
	lastExamined int
}

func NewScheduler() *Scheduler {
	return &Scheduler{
		rooms:  room.NewRegistry(),
		equips: equip.NewPool(),
		byID:   map[string]Surgery{},
	}
}

func (s *Scheduler) AddRoom(id string, turn int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.rooms.Add(id, turn); err != nil {
		if errors.Is(err, room.ErrExists) {
			return ErrExists
		}
		return ErrInvalidArgument
	}
	return nil
}

func (s *Scheduler) AddEquip(id string, count int, sterilize int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.equips.Add(id, count, sterilize); err != nil {
		if errors.Is(err, equip.ErrExists) {
			return ErrExists
		}
		return ErrInvalidArgument
	}
	return nil
}

func (s *Scheduler) Examined() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastExamined
}

func (s *Scheduler) Book(now int64, id, roomID string, start, dur int64, surgeon string, needs Needs) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validTime(now) || !validTime(start) || !validDur(dur) || start+dur > 1_000_000_000 ||
		id == "" || roomID == "" || surgeon == "" || !validNeeds(needs) {
		return ErrInvalidArgument
	}
	if s.haveOp && now < s.now {
		return ErrClockRolledBack
	}
	if _, exists := s.byID[id]; exists {
		return ErrIDExists
	}
	gotRoom, ok := s.rooms.Get(roomID)
	if !ok {
		return ErrUnknownRoom
	}
	if !s.equipmentTypesExist(needs) {
		return ErrUnknownEquip
	}
	for typ, count := range needs {
		typInfo, _ := s.equips.Get(typ)
		if count > typInfo.Count {
			return ErrInvalidArgument
		}
	}

	candidate := Surgery{
		ID:      id,
		Room:    roomID,
		Start:   start,
		Dur:     dur,
		End:     start + dur,
		Surgeon: surgeon,
		Needs:   cloneNeeds(needs),
	}
	windowStart := start - 1680
	windowEnd := start + dur + 1680
	examined := 0
	for _, existing := range s.list {
		if !intersects(windowStart, windowEnd, existing.Start, existing.End) {
			continue
		}
		examined++
		if existing.Room == roomID && room.Conflicts(candidate.Start, candidate.End, existing.Start, existing.End, gotRoom.Turn) {
			s.lastExamined = examined
			return ErrRoomConflict
		}
	}
	for _, existing := range s.list {
		if intersects(candidate.Start, candidate.End, existing.Start, existing.End) && existing.Surgeon == surgeon {
			s.lastExamined = examined
			return ErrSurgeonConflict
		}
	}
	if conflictType, ok := s.equipmentConflict(candidate, nil, windowStart, windowEnd); !ok {
		s.lastExamined = examined
		return TypeError{Err: ErrEquipConflict, Type: conflictType}
	}

	s.advanceTime(now)
	s.insert(candidate)
	s.lastExamined = examined
	return nil
}

func (s *Scheduler) Cancel(now int64, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validTime(now) || id == "" {
		return ErrInvalidArgument
	}
	if s.haveOp && now < s.now {
		return ErrClockRolledBack
	}
	target, ok := s.byID[id]
	if !ok || target.Emergency || target.Start <= now {
		return ErrBadState
	}
	s.advanceTime(now)
	s.remove(id)
	return nil
}

func (s *Scheduler) Emergency(now int64, id string, dur int64, surgeon string, needs Needs) (EmergencyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !validTime(now) || !validDur(dur) || now+dur > 1_000_000_000 || id == "" || surgeon == "" || !validNeeds(needs) {
		return EmergencyResult{}, ErrInvalidArgument
	}
	if s.haveOp && now < s.now {
		return EmergencyResult{}, ErrClockRolledBack
	}
	if _, exists := s.byID[id]; exists {
		return EmergencyResult{}, ErrIDExists
	}
	if !s.equipmentTypesExist(needs) {
		return EmergencyResult{}, ErrUnknownEquip
	}
	for typ, count := range needs {
		typInfo, _ := s.equips.Get(typ)
		if count > typInfo.Count {
			return EmergencyResult{}, ErrInvalidArgument
		}
	}
	rooms := s.rooms.All()
	if len(rooms) == 0 {
		return EmergencyResult{}, ErrNoRoom
	}

	type choice struct {
		room      room.Room
		start     int64
		displaced []Surgery
	}
	var chosen choice
	for _, gotRoom := range rooms {
		start := now
		for _, existing := range s.list {
			if existing.Room != gotRoom.ID || s.isDisplaceable(existing, now) {
				continue
			}
			if existing.End+gotRoom.Turn > start {
				start = existing.End + gotRoom.Turn
			}
		}
		displaced := make([]Surgery, 0)
		for _, existing := range s.list {
			if existing.Room != gotRoom.ID || !s.isDisplaceable(existing, now) {
				continue
			}
			if room.Conflicts(start, start+dur, existing.Start, existing.End, gotRoom.Turn) {
				displaced = append(displaced, existing)
			}
		}
		sort.Slice(displaced, func(i, j int) bool {
			return surgeryLess(displaced[i], displaced[j])
		})
		better := chosen.room.ID == "" ||
			start < chosen.start ||
			(start == chosen.start && len(displaced) < len(chosen.displaced)) ||
			(start == chosen.start && len(displaced) == len(chosen.displaced) && gotRoom.ID < chosen.room.ID)
		if better {
			chosen = choice{room: gotRoom, start: start, displaced: displaced}
		}
	}

	candidate := Surgery{
		ID:        id,
		Room:      chosen.room.ID,
		Start:     chosen.start,
		Dur:       dur,
		End:       chosen.start + dur,
		Surgeon:   surgeon,
		Needs:     cloneNeeds(needs),
		Emergency: true,
	}
	removed := map[string]struct{}{}
	displaced := append([]Surgery(nil), chosen.displaced...)
	for _, surgery := range displaced {
		removed[surgery.ID] = struct{}{}
	}

	surgeonDisplaced := make([]Surgery, 0)
	for _, existing := range s.list {
		if _, already := removed[existing.ID]; already || !intersects(candidate.Start, candidate.End, existing.Start, existing.End) || existing.Surgeon != surgeon {
			continue
		}
		if !s.isDisplaceable(existing, now) {
			return EmergencyResult{}, ErrSurgeonConflict
		}
		surgeonDisplaced = append(surgeonDisplaced, existing)
	}
	sort.Slice(surgeonDisplaced, func(i, j int) bool { return surgeryLess(surgeonDisplaced[i], surgeonDisplaced[j]) })
	for _, surgery := range surgeonDisplaced {
		removed[surgery.ID] = struct{}{}
		displaced = append(displaced, surgery)
	}

	names := make([]string, 0, len(needs))
	for typ := range needs {
		names = append(names, typ)
	}
	sort.Strings(names)
	for _, typ := range names {
		typInfo, _ := s.equips.Get(typ)
		candidateUse := equip.Use{ID: candidate.ID, Start: candidate.Start, End: candidate.End, Count: needs[typ]}
		for {
			uses := make([]equip.Use, 0)
			for _, existing := range s.list {
				if _, skip := removed[existing.ID]; skip || existing.Needs[typ] == 0 {
					continue
				}
				uses = append(uses, equip.Use{ID: existing.ID, Start: existing.Start, End: existing.End, Count: existing.Needs[typ]})
			}
			shortfall := equip.Shortfall(candidateUse, uses, typInfo.Count, typInfo.Sterilize)
			if shortfall <= 0 {
				break
			}
			candidateOccupiedEnd := typInfo.Sterilize + candidate.End
			candidates := make([]Surgery, 0)
			for _, existing := range s.list {
				if _, skip := removed[existing.ID]; skip || !s.isDisplaceable(existing, now) || existing.Needs[typ] == 0 {
					continue
				}
				if equip.Intersects(candidate.Start, candidateOccupiedEnd, existing.Start, existing.End+typInfo.Sterilize) {
					candidates = append(candidates, existing)
				}
			}
			sort.Slice(candidates, func(i, j int) bool {
				if candidates[i].Start != candidates[j].Start {
					return candidates[i].Start > candidates[j].Start
				}
				return candidates[i].ID > candidates[j].ID
			})
			if len(candidates) == 0 {
				return EmergencyResult{}, TypeError{Err: ErrEquipConflict, Type: typ}
			}
			target := candidates[0]
			removed[target.ID] = struct{}{}
			displaced = append(displaced, target)
		}
	}

	s.advanceTime(now)
	for _, surgery := range displaced {
		s.remove(surgery.ID)
	}
	s.insert(candidate)
	return EmergencyResult{Room: chosen.room.ID, Start: chosen.start, Displaced: displaced}, nil
}

func (s *Scheduler) isDisplaceable(surgery Surgery, now int64) bool {
	return !surgery.Emergency && surgery.Start > now
}

func surgeryLess(a, b Surgery) bool {
	if a.Start != b.Start {
		return a.Start < b.Start
	}
	return a.ID < b.ID
}

func (s *Scheduler) advanceTime(now int64) {
	if !s.haveOp || now > s.now {
		s.now = now
	}
	s.haveOp = true
}

func (s *Scheduler) equipmentTypesExist(needs Needs) bool {
	for typ := range needs {
		if _, ok := s.equips.Get(typ); !ok {
			return false
		}
	}
	return true
}

func (s *Scheduler) equipmentConflict(candidate Surgery, removed map[string]struct{}, windowStart, windowEnd int64) (string, bool) {
	names := make([]string, 0, len(candidate.Needs))
	for typ := range candidate.Needs {
		names = append(names, typ)
	}
	sort.Strings(names)
	for _, typ := range names {
		typInfo, _ := s.equips.Get(typ)
		uses := make([]equip.Use, 0)
		for _, existing := range s.list {
			if _, skip := removed[existing.ID]; skip || existing.Needs[typ] == 0 {
				continue
			}
			if existing.Start >= windowEnd || existing.End+typInfo.Sterilize <= windowStart {
				continue
			}
			uses = append(uses, equip.Use{ID: existing.ID, Start: existing.Start, End: existing.End, Count: existing.Needs[typ]})
		}
		candidateUse := equip.Use{ID: candidate.ID, Start: candidate.Start, End: candidate.End, Count: candidate.Needs[typ]}
		if equip.Shortfall(candidateUse, uses, typInfo.Count, typInfo.Sterilize) > 0 {
			return typ, false
		}
	}
	return "", true
}

func (s *Scheduler) insert(surgery Surgery) {
	s.byID[surgery.ID] = surgery
	position := sort.Search(len(s.list), func(i int) bool {
		return s.list[i].Start >= surgery.Start
	})
	s.list = append(s.list, Surgery{})
	copy(s.list[position+1:], s.list[position:])
	s.list[position] = surgery
}

func (s *Scheduler) remove(id string) {
	delete(s.byID, id)
	for i := range s.list {
		if s.list[i].ID == id {
			s.list = append(s.list[:i], s.list[i+1:]...)
			return
		}
	}
}

func validNeeds(needs Needs) bool {
	if len(needs) > 8 {
		return false
	}
	seen := map[string]struct{}{}
	for typ, count := range needs {
		if typ == "" || count < 1 || count > 100 {
			return false
		}
		if _, ok := seen[typ]; ok {
			return false
		}
		seen[typ] = struct{}{}
	}
	return true
}

func validTime(value int64) bool { return value >= 0 && value <= 1_000_000_000 }

func validDur(value int64) bool { return value >= 1 && value <= 1440 }

func cloneNeeds(needs Needs) Needs {
	cloned := make(Needs, len(needs))
	for typ, count := range needs {
		cloned[typ] = count
	}
	return cloned
}

func intersects(startA, endA, startB, endB int64) bool {
	return startA < endB && startB < endA
}
