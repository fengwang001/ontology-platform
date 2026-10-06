package pharmacy

import "sync"

type Options struct {
	ReservationWindow int
}

type System struct {
	mu              sync.Mutex
	now             int
	r               int
	drugs           map[string]*drug
	prescriptions   map[string]*prescription
	activeByLine    map[*line][]*reservation
	events          []*timedEvent
	nextEventOrder  int
	nextReserveID   int
	prescriptionSeq int
}

func NewSystem(options Options) *System {
	return &System{
		r:             options.ReservationWindow,
		drugs:         make(map[string]*drug),
		prescriptions: make(map[string]*prescription),
		activeByLine:  make(map[*line][]*reservation),
	}
}
func (s *System) Dispense(id string, now int) (PrescriptionReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) {
		return PrescriptionReport{}, ErrInvalidArgument
	}
	if now < s.now {
		return PrescriptionReport{}, ErrClockRollback
	}
	p := s.prescriptions[id]
	if p == nil {
		return PrescriptionReport{}, ErrNotFound
	}
	if p.status != StatusActive {
		return PrescriptionReport{}, ErrInvalidState
	}
	if now > p.issuedAt+72*60 {
		return PrescriptionReport{}, ErrPrescriptionExpired
	}
	if !s.canDispenseAfterEvents(p, now) {
		return PrescriptionReport{}, ErrNoActiveReservation
	}
	s.processThrough(now)
	s.now = now
	if p.status != StatusActive {
		return PrescriptionReport{}, ErrInvalidState
	}
	if now > p.issuedAt+72*60 {
		return PrescriptionReport{}, ErrPrescriptionExpired
	}
	if s.totalReserved(p) == 0 {
		return PrescriptionReport{}, ErrNoActiveReservation
	}
	for _, ln := range p.lines {
		quantity := ln.reserved
		if quantity == 0 {
			continue
		}
		d := s.drugs[ln.drugID]
		ln.dispensed += quantity
		d.onHand -= quantity
		d.reserved -= quantity
		ln.reserved = 0
		for _, res := range s.activeByLine[ln] {
			res.quantity = 0
		}
		delete(s.activeByLine, ln)
	}
	if s.isComplete(p) {
		p.status = StatusCompleted
	}
	return s.reportPrescription(p), nil
}

func (s *System) CancelPrescription(id string, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) {
		return ErrInvalidArgument
	}
	if now < s.now {
		return ErrClockRollback
	}
	p := s.prescriptions[id]
	if p == nil {
		return ErrNotFound
	}
	if p.status != StatusActive || now > p.issuedAt+72*60 {
		return s.cancelStateError(p, now)
	}
	s.processThrough(now)
	s.now = now
	if p.status != StatusActive {
		return ErrInvalidState
	}
	p.status = StatusCancelled
	released := make(map[string]*drug)
	for _, ln := range p.lines {
		d := s.drugs[ln.drugID]
		s.removeDebt(d, ln)
		if ln.reserved > 0 {
			s.releaseLineReservations(d, ln)
			released[d.id] = d
		}
	}
	for _, d := range released {
		s.allocateDebts(d, now)
	}
	return nil
}

func (s *System) Prescription(id string, now int) (PrescriptionReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) {
		return PrescriptionReport{}, ErrInvalidArgument
	}
	if now < s.now {
		return PrescriptionReport{}, ErrClockRollback
	}
	p := s.prescriptions[id]
	if p == nil {
		return PrescriptionReport{}, ErrNotFound
	}
	s.processThrough(now)
	s.now = now
	return s.reportPrescription(p), nil
}

func (s *System) Drug(id string, now int) (DrugReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) {
		return DrugReport{}, ErrInvalidArgument
	}
	if now < s.now {
		return DrugReport{}, ErrClockRollback
	}
	d := s.drugs[id]
	if d == nil {
		return DrugReport{}, ErrNotFound
	}
	s.processThrough(now)
	s.now = now
	return DrugReport{OnHand: d.onHand, ActiveReservation: d.reserved, OutstandingDebt: d.debt}, nil
}

func (s *System) dispenseStateError(p *prescription, now int) error {
	if p.status != StatusActive {
		return ErrInvalidState
	}
	if now > p.issuedAt+72*60 {
		return ErrPrescriptionExpired
	}
	return ErrInvalidState
}

func (s *System) cancelStateError(p *prescription, now int) error {
	if p.status != StatusActive {
		return ErrInvalidState
	}
	if now > p.issuedAt+72*60 {
		return ErrInvalidState
	}
	return ErrInvalidState
}

func (s *System) totalReserved(p *prescription) int {
	total := 0
	for _, ln := range p.lines {
		total += ln.reserved
	}
	return total
}

func (s *System) isComplete(p *prescription) bool {
	for _, ln := range p.lines {
		if ln.owed > 0 || ln.reserved > 0 || ln.dispensed < ln.demand {
			return false
		}
	}
	return true
}

func (s *System) reportPrescription(p *prescription) PrescriptionReport {
	report := PrescriptionReport{
		ID:        p.id,
		PatientID: p.patientID,
		Status:    p.status,
		IssuedAt:  p.issuedAt,
		ExpiresAt: p.issuedAt + 72*60,
	}
	for _, ln := range p.lines {
		report.Lines = append(report.Lines, LineReport{
			DrugID:    ln.drugID,
			Demand:    ln.demand,
			Reserved:  ln.reserved,
			Dispensed: ln.dispensed,
			Owed:      ln.owed,
			Complete:  ln.owed == 0 && ln.reserved == 0 && ln.dispensed >= ln.demand,
		})
	}
	return report
}
