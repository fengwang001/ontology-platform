package ontology

type ReservationStatus int

const (
	ReservationReserved ReservationStatus = iota
	ReservationCheckedIn
	ReservationCompleted
	ReservationCancelled
	ReservationNoShow
)

type Reservation struct {
	ID       string
	Elevator string
	House    string
	Start    int
	End      int
	Status   ReservationStatus
	Overdue  bool
}

type slotKey struct {
	elevator string
	minute   int
}

type elevatorScheduler struct {
	records  map[string]*Reservation
	active   map[string]*Reservation
	occupied map[slotKey]*Reservation
	dayBook  map[string]map[int]*Reservation
	heldBy   map[string]string
}

func newElevatorScheduler() *elevatorScheduler {
	return &elevatorScheduler{
		records:  map[string]*Reservation{},
		active:   map[string]*Reservation{},
		occupied: map[slotKey]*Reservation{},
		dayBook:  map[string]map[int]*Reservation{},
		heldBy:   map[string]string{},
	}
}

func (s *elevatorScheduler) reserve(r Reservation) ErrorCode {
	days := s.dayBook[r.House]
	if days == nil {
		days = map[int]*Reservation{}
		s.dayBook[r.House] = days
	}
	day := floorDay(r.Start)
	if days[day] != nil {
		return ErrIllegalState
	}
	for minute := r.Start; minute < r.End; minute++ {
		if s.occupied[slotKey{r.Elevator, minute}] != nil {
			return ErrIllegalState
		}
	}
	rr := r
	s.records[rr.ID] = &rr
	s.active[rr.ID] = &rr
	days[day] = &rr
	for minute := rr.Start; minute < rr.End; minute++ {
		s.occupied[slotKey{rr.Elevator, minute}] = &rr
	}
	return OK
}

func (s *elevatorScheduler) canReserve(r Reservation, now int) ErrorCode {
	day := floorDay(r.Start)
	if owner := s.dayBook[r.House][day]; owner != nil {
		if owner.Status != ReservationReserved || now <= owner.End {
			return ErrIllegalState
		}
	}
	for minute := r.Start; minute < r.End; minute++ {
		occupied := s.occupied[slotKey{r.Elevator, minute}]
		if occupied != nil {
			if occupied.Status != ReservationReserved || now <= occupied.End {
				return ErrIllegalState
			}
		}
	}
	return OK
}

func (s *elevatorScheduler) canCheckIn(id string, now, early int) ErrorCode {
	r := s.active[id]
	if r == nil || r.Status != ReservationReserved {
		return ErrIllegalState
	}
	if now < r.Start-early || now > r.End {
		return ErrTimeWindow
	}
	if holder := s.heldBy[r.Elevator]; holder != "" && holder != r.House {
		return ErrIllegalState
	}
	return OK
}

func (s *elevatorScheduler) slotFree(elevator string, minute int) bool {
	return s.occupied[slotKey{elevator, minute}] == nil
}

func (s *elevatorScheduler) cancel(id string) *Reservation {
	r := s.active[id]
	if r == nil {
		return nil
	}
	r.Status = ReservationCancelled
	s.release(r)
	return r
}

func (s *elevatorScheduler) checkIn(id string) ErrorCode {
	r := s.active[id]
	if r == nil {
		return ErrNotFound
	}
	if s.heldBy[r.Elevator] != "" && s.heldBy[r.Elevator] != r.House {
		return ErrIllegalState
	}
	if r.Status != ReservationReserved {
		return ErrIllegalState
	}
	r.Status = ReservationCheckedIn
	s.heldBy[r.Elevator] = r.House
	return OK
}

func (s *elevatorScheduler) checkOut(id string) ErrorCode {
	r := s.active[id]
	if r == nil {
		return ErrNotFound
	}
	if r.Status != ReservationCheckedIn {
		return ErrIllegalState
	}
	r.Status = ReservationCompleted
	if s.heldBy[r.Elevator] == r.House {
		delete(s.heldBy, r.Elevator)
	}
	s.release(r)
	return OK
}

func (s *elevatorScheduler) advance(now int, onNoShow func(*Reservation)) []*Reservation {
	changed := make([]*Reservation, 0)
	for _, r := range s.active {
		if r.Status == ReservationReserved && now > r.End {
			r.Status = ReservationNoShow
			s.release(r)
			if onNoShow != nil {
				onNoShow(r)
			}
			changed = append(changed, r)
		}
		if r.Status == ReservationCheckedIn && now > r.End {
			s.heldBy[r.Elevator] = r.House
		}
	}
	return changed
}

func (s *elevatorScheduler) release(r *Reservation) {
	for minute := r.Start; minute < r.End; minute++ {
		if s.occupied[slotKey{r.Elevator, minute}] == r {
			delete(s.occupied, slotKey{r.Elevator, minute})
		}
	}
	if dayBook := s.dayBook[r.House]; dayBook != nil {
		delete(dayBook, floorDay(r.Start))
	}
	delete(s.active, r.ID)
}

func (s *elevatorScheduler) get(id string) (*Reservation, bool) {
	r, ok := s.records[id]
	return r, ok
}

func (s *elevatorScheduler) snapshot() []Reservation {
	out := make([]Reservation, 0, len(s.records))
	for _, r := range s.records {
		rr := *r
		if rr.Status == ReservationCheckedIn {
			rr.Overdue = s.heldBy[rr.Elevator] == rr.House
		}
		out = append(out, rr)
	}
	return out
}

func floorDay(minute int) int {
	if minute >= 0 {
		return minute / 1440
	}
	return -((-minute + 1439) / 1440)
}
