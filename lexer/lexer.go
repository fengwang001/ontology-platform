package lexer

import (
	"errors"
	"fmt"
)

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

type Sink interface {
	Start(off int) error
	Append(data []byte) error
	EndField(off int, quoted bool)
	EndRecord(off int)
}

type PosError struct{ Err error; Offset int }

func (e *PosError) Error() string { return fmt.Sprintf("%v at byte %d", e.Err, e.Offset) }
func (e *PosError) Unwrap() error { return e.Err }

var (
	ErrBareQuote = errors.New("unexpected quote in unquoted field")
	ErrAfterQuote = errors.New("unexpected character after quoted field")
	ErrUnclosedQuote = errors.New("unterminated quoted field")
	ErrOrphanCR = errors.New("bare carriage return not followed by newline")
	ErrFieldTooLarge = errors.New("field exceeds byte limit")
	ErrTooManyFields = errors.New("record exceeds field limit")
	ErrTooManyRecords = errors.New("table exceeds record limit")
	ErrTerminal = errors.New("parser is in terminal error state")
)

type State byte
const (
	StartState State = iota
	BareState
	QuotedState
	QSeenState
	CRState
	QCRState
)

type Machine struct {
	State State
	lim Limits
	open, quoted, saw bool
	start, fbytes, fnum, rnum int
	processed int64
	fatal error
}

func New(lim Limits) *Machine { return &Machine{lim: lim} }
func (m *Machine) BytesProcessed() int64 { return m.processed }
func (m *Machine) StateID() State { return m.State }
func (m *Machine) SetState(s State, open bool) { m.State, m.open = s, open }

func (m *Machine) Feed(p []byte, sink Sink) error { return m.run(p, 0, false, sink) }
func (m *Machine) Close(sink Sink) error { return m.run(nil, 0, true, sink) }

func (m *Machine) start(off int, sink Sink) error {
	if m.open { return nil }
	m.fnum++
	if m.lim.MaxFields > 0 && m.fnum > m.lim.MaxFields { return m.fail(ErrTooManyFields, off) }
	m.open, m.quoted, m.start, m.fbytes = true, false, off, 0
	return sink.Start(off)
}

func (m *Machine) append(data []byte, off int, sink Sink) error {
	m.fbytes += len(data)
	if m.lim.MaxFieldBytes > 0 && m.fbytes > m.lim.MaxFieldBytes { return m.fail(ErrFieldTooLarge, off) }
	return sink.Append(data)
}

func (m *Machine) endField(off int, quoted bool, sink Sink) {
	sink.EndField(off, quoted || m.quoted)
	m.open, m.quoted, m.fbytes, m.State = false, false, 0, StartState
}

func (m *Machine) endRecord(off int, sink Sink) error {
	m.endField(off, m.quoted, sink)
	m.fnum = 0
	m.rnum++
	if m.lim.MaxRecords > 0 && m.rnum > m.lim.MaxRecords { return m.fail(ErrTooManyRecords, off) }
	sink.EndRecord(off)
	return nil
}

func (m *Machine) fail(err error, off int) error { return &PosError{Err: err, Offset: off} }
