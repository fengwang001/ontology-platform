package naive

func (m *Model) check(at int64) bool { return at >= m.now }

func (m *Model) Create(id, source, dest string, lines []Line, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || source == "" || dest == "" || source == dest || at < 0 {
		return ec(CodeInvalidArgument)
	}
	seen := map[string]bool{}
	if len(lines) == 0 {
		return ec(CodeInvalidArgument)
	}
	for _, ln := range lines {
		if ln.Item == "" || ln.Qty <= 0 || seen[ln.Item] {
			return ec(CodeInvalidArgument)
		}
		seen[ln.Item] = true
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	if _, ok := m.orders[id]; ok {
		return ecState(CodeInvalidState, m.orders[id].status)
	}
	minIdx := -1
	for i, ln := range lines {
		if m.a(source, ln.Item) < ln.Qty {
			minIdx = i
			break
		}
	}
	if minIdx != -1 {
		return ec(CodeInsufficientStock)
	}
	for _, ln := range lines {
		m.addAvail(source, ln.Item, -ln.Qty)
		m.addFrozen(source, ln.Item, ln.Qty)
	}
	o := &order{id: id, source: source, dest: dest, status: StatusCreated}
	for _, ln := range lines {
		o.lines = append(o.lines, LineState{Item: ln.Item, Requested: ln.Qty})
	}
	m.orders[id] = o
	m.now = at
	return nil
}

func (m *Model) Cancel(id string, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || at < 0 {
		return ec(CodeInvalidArgument)
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	o, ok := m.orders[id]
	if !ok {
		return ec(CodeNotFound)
	}
	if o.status != StatusCreated {
		return ecState(CodeInvalidState, o.status)
	}
	for _, ln := range o.lines {
		m.addFrozen(o.source, ln.Item, -ln.Requested)
		m.addAvail(o.source, ln.Item, ln.Requested)
	}
	o.status = StatusCancelled
	m.now = at
	return nil
}

func (m *Model) Ship(id string, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || at < 0 {
		return ec(CodeInvalidArgument)
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	o, ok := m.orders[id]
	if !ok {
		return ec(CodeNotFound)
	}
	if o.status != StatusCreated {
		return ecState(CodeInvalidState, o.status)
	}
	for i := range o.lines {
		ln := &o.lines[i]
		ln.Issued = ln.Requested
		m.addFrozen(o.source, ln.Item, -ln.Requested)
	}
	o.status = StatusShipped
	o.shipTime = at
	m.now = at
	return nil
}

func (m *Model) Receive(id, item string, qty, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || item == "" || qty <= 0 || at < 0 {
		return ec(CodeInvalidArgument)
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	o, ok := m.orders[id]
	if !ok {
		return ec(CodeNotFound)
	}
	if o.status != StatusShipped {
		return ecState(CodeInvalidState, o.status)
	}
	idx := -1
	for i := range o.lines {
		if o.lines[i].Item == item {
			idx = i
		}
	}
	if idx == -1 {
		return ec(CodeInvalidArgument)
	}
	ln := &o.lines[idx]
	tolerance := ln.Issued * m.tolerance / 1000
	if ln.Received+qty > ln.Issued+tolerance {
		return ec(CodeOverReceipt)
	}
	ln.Received += qty
	m.addAvail(o.dest, item, qty)
	m.now = at
	return nil
}

func (m *Model) Close(id string, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || at < 0 {
		return ec(CodeInvalidArgument)
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	o, ok := m.orders[id]
	if !ok {
		return ec(CodeNotFound)
	}
	if o.status != StatusShipped {
		return ecState(CodeInvalidState, o.status)
	}
	all := true
	for i := range o.lines {
		if o.lines[i].Received < o.lines[i].Issued {
			all = false
		}
	}
	if !all && at < o.shipTime+m.wait {
		return ec(CodeCloseTooEarly)
	}
	for i := range o.lines {
		ln := &o.lines[i]
		switch {
		case ln.Received < ln.Issued:
			ln.Shortage = ln.Issued - ln.Received
		case ln.Received > ln.Issued:
			ln.Overage = ln.Received - ln.Issued
		}
	}
	o.status = StatusClosed
	m.now = at
	return nil
}

func (m *Model) Recover(id, item string, qty, at int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if id == "" || item == "" || qty <= 0 || at < 0 {
		return ec(CodeInvalidArgument)
	}
	if !m.check(at) {
		return ec(CodeClockRollback)
	}
	o, ok := m.orders[id]
	if !ok {
		return ec(CodeNotFound)
	}
	if o.status != StatusClosed {
		return ecState(CodeInvalidState, o.status)
	}
	idx := -1
	for i := range o.lines {
		if o.lines[i].Item == item {
			idx = i
		}
	}
	if idx == -1 {
		return ec(CodeInvalidArgument)
	}
	ln := &o.lines[idx]
	if ln.Shortage == 0 {
		return ec(CodeNoShortage)
	}
	if qty > ln.Shortage {
		return ec(CodeRecoveryExceed)
	}
	ln.Shortage -= qty
	m.addAvail(o.dest, item, qty)
	m.now = at
	return nil
}

func (m *Model) Stock(wh, item string) (int64, int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.a(wh, item), m.f(wh, item)
}

func (m *Model) OrderLines(id string) ([]LineState, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return nil, false
	}
	out := make([]LineState, len(o.lines))
	copy(out, o.lines)
	return out, true
}

func (m *Model) OrderStatus(id string) (Status, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orders[id]
	if !ok {
		return 0, false
	}
	return o.status, true
}

// VerifyItem 核验守恒：可用+冻结+在途+短缺 == 初始总量+超收盈余。
func (m *Model) VerifyItem(item string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	var avail, frozen, transit, shortage, overage int64
	for wh := range m.avail {
		avail += m.a(wh, item)
	}
	for wh := range m.frozen {
		frozen += m.f(wh, item)
	}
	for _, o := range m.orders {
		for _, ln := range o.lines {
			if ln.Item != item {
				continue
			}
			switch o.status {
			case StatusShipped:
				if ln.Received < ln.Issued {
					transit += ln.Issued - ln.Received
				} else if ln.Received > ln.Issued {
					overage += ln.Received - ln.Issued
				}
			case StatusClosed:
				shortage += ln.Shortage
				overage += ln.Overage
			}
		}
	}
	return avail+frozen+transit+shortage == m.initial[item]+overage
}
