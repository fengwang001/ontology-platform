package pharmacy

import "container/list"

func validID(id string) bool {
	return id != ""
}

func validQuantity(quantity int) bool {
	return quantity >= minQty && quantity <= maxQty
}

func validTime(now int) bool {
	return now >= minTime && now <= maxTime
}

func (s *System) checkOperation(id string, now int) (*drug, error) {
	if !validID(id) || !validTime(now) || now < s.now {
		return nil, s.checkBasicError(id, now)
	}
	d := s.drugs[id]
	if d == nil {
		return nil, ErrNotFound
	}
	return d, nil
}

func (s *System) checkBasicError(id string, now int) error {
	if !validID(id) || !validTime(now) {
		return ErrInvalidArgument
	}
	if now < s.now {
		return ErrClockRollback
	}
	return nil
}

func (s *System) RegisterDrug(id string, config DrugConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || config.PackSize < 1 || config.PackSize > maxQty {
		return ErrInvalidArgument
	}
	if _, exists := s.drugs[id]; exists {
		return ErrInvalidArgument
	}
	d := &drug{id: id, packSize: config.PackSize, splittable: config.Splittable}
	d.queue = list.New()
	s.drugs[id] = d
	return nil
}

func (s *System) ReceiveDrug(id string, now, quantity int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !validID(id) || !validTime(now) || !validQuantity(quantity) {
		return s.receiveBasicError(id, now, quantity)
	}
	if now < s.now {
		return ErrClockRollback
	}
	d, exists := s.drugs[id]
	if !exists {
		return ErrNotFound
	}
	s.processThrough(now)
	s.now = now
	d.onHand += quantity
	s.allocateDebts(d, now)
	return nil
}

func (s *System) receiveBasicError(id string, now, quantity int) error {
	if !validID(id) || !validTime(now) || !validQuantity(quantity) {
		return ErrInvalidArgument
	}
	if now < s.now {
		return ErrClockRollback
	}
	return nil
}

func (s *System) AcceptPrescription(id string, input PrescriptionInput, now int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validatePrescriptionInput(id, input, now); err != nil {
		return err
	}
	if _, exists := s.prescriptions[id]; exists {
		return ErrInvalidArgument
	}
	drugsInOrder := make([]*drug, len(input.Lines))
	for i, item := range input.Lines {
		d := s.drugs[item.DrugID]
		if d == nil {
			return ErrNotFound
		}
		drugsInOrder[i] = d
	}
	if input.IssuedAt+72*60 < now {
		return ErrPrescriptionExpired
	}
	s.processThrough(now)
	s.now = now
	if input.AllAtOnce {
		for i, item := range input.Lines {
			d := drugsInOrder[i]
			needed := item.Quantity
			if !d.splittable {
				needed = roundUp(needed, d.packSize)
			}
			if d.available() < needed {
				return ErrOutOfStock
			}
		}
	}
	s.prescriptionSeq++
	p := &prescription{
		id:        id,
		patientID: input.PatientID,
		issuedAt:  input.IssuedAt,
		allAtOnce: input.AllAtOnce,
		status:    StatusActive,
		order:     s.prescriptionSeq,
	}
	for _, item := range input.Lines {
		p.lines = append(p.lines, &line{p: p, drugID: item.DrugID, demand: item.Quantity})
	}
	s.prescriptions[id] = p
	s.pushEvent(input.IssuedAt+72*60+1, prescriptionExpiry{p: p})
	for i, ln := range p.lines {
		d := drugsInOrder[i]
		available := d.available()
		quantity := reservationQuantity(d, ln.demand, available)
		if quantity > 0 {
			s.addReservation(d, ln, quantity, now)
		}
		if ln.demand-quantity > 0 {
			ln.owed = ln.demand - quantity
			d.debt += ln.owed
			s.enqueueDebt(d, p, ln)
		}
	}
	return nil
}

func (s *System) validatePrescriptionInput(id string, input PrescriptionInput, now int) error {
	if !validID(id) || !validID(input.PatientID) || !validTime(now) || !validTime(input.IssuedAt) ||
		input.IssuedAt > now || len(input.Lines) < 1 || len(input.Lines) > 8 {
		return s.prescriptionParamError(id, input, now)
	}
	seen := make(map[string]struct{}, len(input.Lines))
	for _, item := range input.Lines {
		if !validID(item.DrugID) || !validQuantity(item.Quantity) {
			return ErrInvalidArgument
		}
		if _, duplicate := seen[item.DrugID]; duplicate {
			return ErrInvalidArgument
		}
		seen[item.DrugID] = struct{}{}
	}
	if now < s.now {
		return ErrClockRollback
	}
	return nil
}

func (s *System) prescriptionParamError(id string, input PrescriptionInput, now int) error {
	_ = id
	_ = input
	_ = now
	return ErrInvalidArgument
}
