package lexer

func (m *Machine) run(p []byte, base int, eof bool, sink Sink) (err error) {
	if m.fatal != nil { return m.fatal }
	defer func() { if err != nil { m.fatal = err } }()
	for i := 0; i < len(p); i++ {
		b, off := p[i], base+i
		m.saw, m.processed = true, m.processed+1
		if err = m.step(b, off, sink); err != nil { return err }
	}
	if !eof { return nil }
	switch m.State {
	case QuotedState, QSeenState:
		return m.fail(ErrUnclosedQuote, m.start)
	case CRState, QCRState:
		return m.fail(ErrOrphanCR, base+len(p))
	case BareState:
		m.endField(base+len(p), false, sink)
	case StartState:
		if m.saw || m.rnum > 0 {
			if err = m.start(base+len(p), sink); err != nil { return }
			m.endField(base+len(p), false, sink)
		}
	}
	return nil
}

func (m *Machine) step(b byte, off int, sink Sink) error {
	switch m.State {
	case StartState:
		return m.stepStart(b, off, sink)
	case BareState:
		return m.stepBare(b, off, sink)
	case QuotedState:
		if b == '"' { m.State = QSeenState; return nil }
		return m.append([]byte{b}, off, sink)
	case QSeenState:
		return m.stepQSeen(b, off, sink)
	case CRState, QCRState:
		if b != '\n' { return m.fail(ErrOrphanCR, off) }
		return m.endRecord(off+1, sink)
	}
	return nil
}

func (m *Machine) stepStart(b byte, off int, sink Sink) error {
	switch {
	case b == ',':
		if err := m.start(off, sink); err != nil { return err }
		m.endField(off+1, false, sink)
	case b == '\n':
		if err := m.start(off, sink); err != nil { return err }
		return m.endRecord(off+1, sink)
	case b == '\r':
		if err := m.start(off, sink); err != nil { return err }
		m.State = CRState
	case b == '"':
		if err := m.start(off, sink); err != nil { return err }
		m.quoted, m.State = true, QuotedState
	default:
		if err := m.start(off, sink); err != nil { return err }
		m.State = BareState
		return m.append([]byte{b}, off, sink)
	}
	return nil
}

func (m *Machine) stepBare(b byte, off int, sink Sink) error {
	switch {
	case b == ',':
		m.endField(off, false, sink)
	case b == '\n':
		return m.endRecord(off+1, sink)
	case b == '\r':
		m.State = CRState
	case b == '"':
		return m.fail(ErrBareQuote, off)
	default:
		return m.append([]byte{b}, off, sink)
	}
	return nil
}

func (m *Machine) stepQSeen(b byte, off int, sink Sink) error {
	switch b {
	case '"':
		m.State = QuotedState
		return m.append([]byte{'"'}, off, sink)
	case ',':
		m.endField(off, true, sink)
	case '\n':
		return m.endRecord(off+1, sink)
	case '\r':
		m.State = QCRState
	default:
		return m.fail(ErrAfterQuote, off)
	}
	return nil
}
