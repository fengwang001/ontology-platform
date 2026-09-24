package lexer

import (
	"errors"
	"ontology/cell"
	"strings"
)

var (
	ErrBareQuote      = errors.New("bare double quote")
	ErrTextAfterQuote = errors.New("text after closed quote")
	ErrUnclosedQuote  = errors.New("unclosed quoted field")
	ErrBareCR         = errors.New("bare carriage return")
	ErrFieldTooLarge  = errors.New("field exceeds byte limit")
	ErrTooManyFields  = errors.New("record exceeds field limit")
	ErrTooManyRecords = errors.New("record count exceeds limit")
	ErrClosed         = errors.New("lexer is closed")
)

type Error struct {
	Kind   error
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

const (
	startField = iota
	bareField
	quotedField
	quoteSeen
	crPending
)

// Limits are enforced by the byte state machine.
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
	MaxRecords    int
}

type EventFunc func(cell.Cell)
type RecordFunc func(endOffset int)

// Lexer is a resumable RFC 4180-dialect scanner.
type Lexer struct {
	lim      Limits
	field EventFunc
	record RecordFunc
	state, offset, fieldStart, fieldEnd int
	value                               strings.Builder
	quoted, openRecord, closed, fatal   bool
	fieldNo, records, byteCount         int
	err                                 *Error
}

func New(lim Limits, field EventFunc, record RecordFunc) *Lexer {
	return &Lexer{lim: lim, field: field, record: record}
}

func (l *Lexer) Feed(p []byte) error {
	if l.fatal {
		return l.err
	}
	if l.closed {
		return ErrClosed
	}
	for _, b := range p {
		if err := l.step(b); err != nil {
			return err
		}
	}
	return nil
}

func (l *Lexer) Close() error {
	if l.fatal {
		return l.err
	}
	l.closed = true
	switch l.state {
	case quotedField:
		return l.fail(ErrUnclosedQuote, l.fieldStart, l.records+1, l.fieldNo+1)
	case crPending:
		return l.fail(ErrBareCR, l.offset-1, l.records+1, l.fieldNo+1)
	}
	if l.openRecord {
		l.emitField(l.offset)
		l.emitRecord(l.offset)
	}
	return nil
}

func (l *Lexer) ByteCount() int { return l.byteCount }

func (l *Lexer) step(b byte) error {
	l.byteCount++
	off := l.offset
	l.offset++
	switch l.state {
	case startField:
		return l.atStart(b, off)
	case bareField:
		switch b {
		case ',':
			l.finishField(off)
			return l.comingField()
		case '"':
			return l.fail(ErrBareQuote, off, l.records+1, l.fieldNo+1)
		case '\r':
			l.state = crPending
		case '\n':
			l.finishLine(off)
		default:
			if err := l.addByte(b); err != nil {
				return err
			}
		}
	case quotedField:
		switch b {
		case '"':
			l.fieldEnd = off + 1
			l.state = quoteSeen
		default:
			if err := l.addByte(b); err != nil {
				return err
			}
		}
	case quoteSeen:
		switch b {
		case '"':
			l.state = quotedField
			if err := l.addByte('"'); err != nil {
				return err
			}
		case ',':
			l.finishField(off)
			return l.comingField()
		case '\r':
			l.state = crPending
		case '\n':
			l.finishLine(off)
		default:
			return l.fail(ErrTextAfterQuote, off, l.records+1, l.fieldNo+1)
		}
	case crPending:
		if b == '\n' {
			l.finishLine(off + 1)
			if l.fatal {
				return l.err
			}
		} else {
			return l.fail(ErrBareCR, off-1, l.records+1, l.fieldNo+1)
		}
	}
	return nil
}

func (l *Lexer) atStart(b byte, off int) error {
	if !l.openRecord {
		l.openRecord = true
		l.fieldStart = off
	}
	switch b {
	case ',':
		l.finishField(off)
		return l.comingField()
	case '"':
		l.quoted = true
		l.state = quotedField
	case '\r':
		l.state = crPending
	case '\n':
		l.finishLine(off)
	default:
		l.state = bareField
		return l.addByte(b)
	}
	return nil
}

func (l *Lexer) addByte(b byte) error {
	if l.lim.MaxFieldBytes > 0 && l.value.Len() >= l.lim.MaxFieldBytes {
		return l.fail(ErrFieldTooLarge, l.offset-1, l.records+1, l.fieldNo+1)
	}
	l.value.WriteByte(b)
	return nil
}

func (l *Lexer) comingField() error {
	if l.lim.MaxFields > 0 && l.fieldNo >= l.lim.MaxFields {
		return l.fail(ErrTooManyFields, l.offset-1, l.records+1, l.fieldNo+1)
	}
	l.fieldNo++
	l.fieldStart = l.offset
	l.fieldEnd = 0
	l.quoted = false
	l.value.Reset()
	l.state = startField
	return nil
}

func (l *Lexer) finishField(end int) {
	if l.quoted && l.state == quoteSeen {
		end = l.fieldEnd
	}
	l.emitField(end)
}

func (l *Lexer) emitField(end int) {
	if l.field != nil {
		l.field(cell.Cell{Value: l.value.String(), Quoted: l.quoted, Start: l.fieldStart, End: end})
	}
	l.fieldNo++
}

func (l *Lexer) finishLine(end int) {
	l.finishField(end)
	_ = l.emitRecord(end)
	l.openRecord = false
	l.quoted = false
	l.fieldNo = 0
	l.value.Reset()
	l.state = startField
}

func (l *Lexer) emitRecord(end int) error {
	l.records++
	if l.lim.MaxRecords > 0 && l.records > l.lim.MaxRecords {
		return l.fail(ErrTooManyRecords, end, l.records, l.fieldNo)
	}
	if l.record != nil {
		l.record(end)
	}
	return nil
}

func (l *Lexer) fail(k error, off, rec, field int) error {
	l.fatal = true
	l.err = &Error{Kind: k, Offset: off, Record: rec, Field: field}
	return l.err
}
