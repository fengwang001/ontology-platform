package pharmacy

type debtEntry struct {
	p            *prescription
	ln           *line
	order        int
	registeredAt int
	registerSeq  int
}

func (s *System) enqueueDebt(d *drug, p *prescription, ln *line) {
	if ln.owed <= 0 || ln.queueElem != nil {
		return
	}
	d.queueSeq++
	entry := &debtEntry{p: p, ln: ln, order: p.order, registerSeq: d.queueSeq}
	ln.queueElem = d.queue.PushBack(entry)
}

func (s *System) removeDebt(d *drug, ln *line) {
	d.debt -= ln.owed
	if ln.queueElem != nil {
		d.queue.Remove(ln.queueElem)
		ln.queueElem = nil
	}
	ln.owed = 0
}

func (s *System) insertDebtByAcceptOrder(d *drug, p *prescription, ln *line, at int) {
	if ln.owed <= 0 || ln.queueElem != nil {
		return
	}
	d.queueSeq++
	entry := &debtEntry{p: p, ln: ln, order: p.order, registeredAt: at, registerSeq: d.queueSeq}
	element := d.queue.Front()
	for element != nil {
		existing := element.Value.(*debtEntry)
		if (existing.registeredAt == 0 && existing.order <= p.order) ||
			existing.registeredAt < at ||
			(existing.registeredAt == at && existing.registerSeq < entry.registerSeq) {
			element = element.Next()
			continue
		}
		break
	}
	if element == nil {
		ln.queueElem = d.queue.PushBack(entry)
	} else {
		ln.queueElem = d.queue.InsertBefore(entry, element)
	}
}

func (s *System) enqueueExistingDebt(d *drug, p *prescription, ln *line) {
	if ln.owed > 0 && ln.queueElem == nil {
		s.enqueueDebt(d, p, ln)
	}
}

func (s *System) allocateDebts(d *drug, at int) {
	if d.queue.Len() == 0 || d.available() <= 0 {
		return
	}
	element := d.queue.Front()
	for element != nil {
		next := element.Next()
		entry := element.Value.(*debtEntry)
		if entry.p.status != StatusActive || entry.ln.owed <= 0 {
			d.queue.Remove(element)
			entry.ln.queueElem = nil
			element = next
			continue
		}
		available := d.available()
		quantity := reservationQuantity(d, entry.ln.owed, available)
		if quantity > 0 {
			s.addReservation(d, entry.ln, quantity, at)
			allocated := min(quantity, entry.ln.owed)
			entry.ln.owed -= allocated
			d.debt -= allocated
		}
		if entry.ln.owed <= 0 && quantity > 0 {
			d.queue.Remove(element)
			entry.ln.queueElem = nil
		}
		if d.available() <= 0 || (!d.splittable && quantity == 0) {
			return
		}
		element = next
	}
}
