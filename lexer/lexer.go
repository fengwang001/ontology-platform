package lexer

import "ontology/cell"

type st uint8

const (
	sFS st = iota
	sUnq
	sQuot
	sAQ
	sCR // pending CR after an unquoted/closed field
	sCRA
)

// Machine is a single-use state machine; not concurrency safe.
type Machine struct {
	lim               Limits
	st                st
	buf               []byte
	start, off, crAt  int64
	fsz               int
	quoted, open, rec bool
	events            []Event
	fatal             *Error
	count             int64
	watch             int
	found             int64
}

// New builds a machine; Start{Mode: MFresh} is an ordinary fresh stream.
func New(lim Limits, s Start) *Machine {
	m := &Machine{lim: lim, st: sFS, start: s.First, fsz: s.Size, found: -1}
	switch s.Mode {
	case MUnq:
		m.st, m.open, m.rec = sUnq, true, true
	case MQuot:
		m.st, m.open, m.quoted, m.rec = sQuot, true, true, true
	case MAQ:
		m.st, m.open, m.quoted, m.rec = sAQ, true, true, true
	}
	return m
}

// Watch records once the global offset of the nth content byte (1-based) in the
// spanning field; Found reads it. Used by par to localize size breaches.
func (m *Machine) Watch(n int)     { m.watch = n }
func (m *Machine) Found() int64    { return m.found }
func (m *Machine) Count() int64    { return m.count }
func (m *Machine) Events() []Event { return m.events }
func (m *Machine) Fatal() *Error   { return m.fatal }

func (m *Machine) fail(k error) bool {
	if m.fatal == nil {
		m.fatal = &Error{Err: k, Offset: m.off - 1}
	}
	return false
}

func (m *Machine) grow(b byte) bool {
	m.fsz++
	if m.watch > 0 && m.fsz == m.watch {
		m.found = m.off - 1
	}
	if m.lim.MaxFieldBytes > 0 && m.fsz > m.lim.MaxFieldBytes {
		return m.fail(ErrFieldTooLarge)
	}
	m.buf = append(m.buf, b)
	return true
}

func (m *Machine) emit(row bool, end int64) {
	m.events = append(m.events, Event{Cell: cell.Cell{
		Value: string(m.buf), Quoted: m.quoted, Start: m.start, End: end,
	}, Row: row})
	m.buf, m.fsz, m.quoted, m.open = m.buf[:0], 0, false, false
	m.start, m.rec = m.off, row
}

// Feed pushes one chunk; after a fatal error it returns the same error.
func (m *Machine) Feed(p []byte) error {
	if m.fatal != nil {
		return m.fatal
	}
	for _, b := range p {
		at := m.off
		m.off, m.count = m.off+1, m.count+1
		if !m.stepAt(b, at) {
			return m.fatal
		}
	}
	return nil
}

func (m *Machine) stepAt(b byte, at int64) bool {
	switch m.st {
	case sFS:
		switch b {
		case ',':
			m.emit(false, at)
		case '"':
			m.st, m.open, m.quoted, m.start, m.rec = sQuot, true, true, at, true
		case '\r':
			m.st, m.crAt = sCR, at
		case '\n':
			if m.rec {
				m.emit(true, at)
			}
		default:
			m.st, m.open, m.start, m.rec = sUnq, true, at, true
			return m.grow(b)
		}
		return true
	case sUnq:
		switch b {
		case ',':
			m.emit(false, at)
			m.st = sFS
		case '"':
			return m.fail(ErrQuoteInField)
		case '\r':
			m.st, m.crAt = sCR, at
		case '\n':
			m.emit(true, at)
			m.st = sFS
		default:
			return m.grow(b)
		}
		return true
	case sQuot:
		if b == '"' {
			m.st = sAQ
			return true
		}
		return m.grow(b)
	case sAQ:
		switch b {
		case '"':
			m.st = sQuot
			return m.grow('"')
		case ',':
			m.emit(false, at)
			m.st = sFS
		case '\r':
			m.st, m.crAt = sCRA, at
		case '\n':
			m.emit(true, at)
			m.st = sFS
		default:
			return m.fail(ErrCharsAfterQuote)
		}
		return true
	case sCR, sCRA:
		if b == '\n' {
			m.emit(true, m.crAt)
			m.st = sFS
			return true
		}
		if m.st == sCR {
			return m.fail(ErrBareCR)
		}
		return m.fail(ErrCharsAfterQuote)
	}
	return true
}

// Close finalizes: a trailing unterminated record is emitted, except for an
// empty input or a blank trailing line.
func (m *Machine) Close() error {
	if m.fatal != nil {
		return m.fatal
	}
	switch m.st {
	case sQuot:
		m.fail(ErrUnclosedQuote)
	case sCR, sCRA:
		m.fail(ErrBareCR)
	}
	if m.fatal == nil && (m.open || m.rec) {
		m.emit(true, m.off)
	}
	if m.fatal != nil {
		return m.fatal
	}
	return nil
}

// StateAtEnd reports the end mode and whether a field is spanning.
func (m *Machine) StateAtEnd() (Mode, bool) {
	switch m.st {
	case sUnq:
		return MUnq, true
	case sQuot:
		return MQuot, true
	case sAQ:
		return MAQ, true
	}
	return MFresh, false
}

// Pending returns the not-yet-emitted spanning field.
func (m *Machine) Pending() ([]byte, bool, int64, int64, bool) {
	return m.buf, m.quoted, m.start, m.off, m.open
}
