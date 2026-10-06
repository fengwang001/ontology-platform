package naive

func (e *Engine) remOf(o *orderView) int64 { return o.total - o.filled }

// Cancel removes an order.
func (e *Engine) Cancel(id string) *Error {
	o, ok := e.orders[id]
	if !ok {
		return &Error{ErrNotFound, "not found"}
	}
	if o.cancelled || e.remOf(o) == 0 {
		return &Error{ErrFinished, "finished"}
	}
	e.purge(o)
	o.cancelled = true
	return nil
}

func (e *Engine) purge(o *orderView) {
	l := e.levels[o.side][o.price]
	if l == nil {
		return
	}
	vis := l.visible[:0]
	for _, s := range l.visible {
		if s.ID != o.id {
			vis = append(vis, s)
		}
	}
	l.visible = vis
	hid := l.hidden[:0]
	for _, h := range l.hidden {
		if h.ID != o.id {
			hid = append(hid, h)
		}
	}
	l.hidden = hid
	delete(l.reserve, o.id)
	if l.isEmpty() {
		delete(e.levels[o.side], o.price)
	}
}

// Modify changes remaining quantity: a decrease keeps position, an increase
// loses priority and re-enters at the tail with a fresh sequence.
func (e *Engine) Modify(id string, newRem int64) *Error {
	if newRem <= 0 {
		return &Error{ErrInvalid, "non-positive"}
	}
	o, ok := e.orders[id]
	if !ok {
		return &Error{ErrNotFound, "not found"}
	}
	if o.cancelled || e.remOf(o) == 0 {
		return &Error{ErrFinished, "finished"}
	}
	cur := e.remOf(o)
	if newRem < cur {
		e.decrease(o, newRem)
	} else if newRem > cur {
		e.purge(o)
		o.total = o.filled + newRem
		o.seq = e.nextSeq()
		e.rest(o)
	}
	return nil
}

func (e *Engine) decrease(o *orderView, newRem int64) {
	o.total = o.filled + newRem
	l := e.levels[o.side][o.price]
	if o.kind == Hidden {
		for i := range l.hidden {
			if l.hidden[i].ID == o.id && newRem < l.hidden[i].Size {
				l.hidden[i].Size = newRem
			}
		}
		return
	}
	for i := range l.visible {
		if l.visible[i].ID != o.id {
			continue
		}
		if newRem < l.visible[i].Size {
			l.visible[i].Size = newRem
			delete(l.reserve, o.id)
		} else if o.kind == Iceberg {
			if r := newRem - l.visible[i].Size; r > 0 {
				l.reserve[o.id] = r
			} else {
				delete(l.reserve, o.id)
			}
		}
	}
}
