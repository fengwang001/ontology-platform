package lexer

import (
	"errors"
	"fmt"
	"ontology/cell"
)

const (
	KindBareQuote = "bare quote"
	KindJunkQuote = "text after quoted field"
	KindUnclosed  = "unterminated quoted field"
	KindBareCR    = "bare carriage return"
	KindFieldSize = "field too large"
)

var (
	ErrBareQuote = errors.New("lexer: bare quote in unquoted field")
	ErrJunkQuote = errors.New("lexer: text after closed quoted field")
	ErrUnclosed  = errors.New("lexer: unterminated quoted field")
	ErrBareCR    = errors.New("lexer: bare carriage return")
	ErrFieldSize = errors.New("lexer: field exceeds byte limit")
)

type Limits struct{ MaxFieldBytes int }

type Error struct {
	Err    error
	Kind   string
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s at byte %d, record %d, field %d", e.Kind, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.Err }

type Event struct {
	Cell cell.Cell
	Last bool
}

type Handler func(Event) error

const (
	stStart = iota
	stUnquoted
	stQuoted
	stQuoteEnd
	stCR
)

type Lexer struct {
	limits                     Limits
	handler                    Handler
	state                      int
	value                      []byte
	pos, start, record, fields int
	quoted, started            bool
	count                      int
	err                        error
}

func New(limits Limits, handler Handler) *Lexer {
	return &Lexer{limits: limits, handler: handler, record: 1}
}
func (l *Lexer) BytesProcessed() int { return l.count }

func (l *Lexer) fail(err error, kind string, offset int) error {
	if l.err == nil {
		l.err = &Error{Err: err, Kind: kind, Offset: offset, Record: l.record, Field: l.fields + 1}
	}
	return l.err
}

func (l *Lexer) begin(quoted bool, offset int) {
	l.started, l.quoted, l.start, l.value = true, quoted, offset, l.value[:0]
}

func (l *Lexer) add(b byte, offset int) error {
	if l.limits.MaxFieldBytes > 0 && len(l.value) >= l.limits.MaxFieldBytes {
		return l.fail(ErrFieldSize, KindFieldSize, offset)
	}
	l.value = append(l.value, b)
	return nil
}

func (l *Lexer) emit(last bool, offset int) error {
	ev := Event{Last: last, Cell: cell.Cell{Value: append([]byte(nil), l.value...), Quoted: l.quoted, Start: l.start, End: offset}}
	l.fields++
	l.started = false
	return l.handler(ev)
}

func (l *Lexer) endRecord(end, next int) error {
	if !l.started {
		l.begin(false, end)
	}
	if err := l.emit(true, end); err != nil {
		l.err = err
		return err
	}
	l.record, l.fields, l.state = next, 0, stStart
	return nil
}

func (l *Lexer) Feed(p []byte) error {
	if l.err != nil {
		return l.err
	}
	for _, b := range p {
		offset := l.pos
		l.pos, l.count = l.pos+1, l.count+1
		switch l.state {
		case stStart, stQuoteEnd:
			switch {
			case b == ',':
				if l.state == stQuoteEnd {
					if err := l.emit(false, offset); err != nil {
						l.err = err
						return err
					}
				} else if !l.started {
					l.begin(false, offset)
					if err := l.emit(false, offset); err != nil {
						l.err = err
						return err
					}
				}
				l.state = stStart
			case b == '\n':
				if l.state == stQuoteEnd {
					if err := l.endRecord(offset, l.record+1); err != nil {
						return err
					}
				} else if l.fields == 0 && !l.started {
					l.record++
					l.state = stStart
				} else {
					if err := l.endRecord(offset, l.record+1); err != nil {
						return err
					}
				}
			case b == '\r':
				if l.state == stQuoteEnd {
					return l.fail(ErrJunkQuote, KindJunkQuote, offset)
				}
				l.state = stCR
			case b == '"':
				if l.state == stQuoteEnd {
					return l.fail(ErrJunkQuote, KindJunkQuote, offset)
				}
				l.begin(true, offset)
				l.state = stQuoted
			default:
				if l.state == stQuoteEnd {
					return l.fail(ErrJunkQuote, KindJunkQuote, offset)
				}
				l.begin(false, offset)
				if err := l.add(b, offset); err != nil {
					return err
				}
				l.state = stUnquoted
			}
		case stUnquoted:
			switch {
			case b == ',':
				if err := l.emit(false, offset); err != nil {
					l.err = err
					return err
				}
				l.state = stStart
			case b == '\n':
				if err := l.endRecord(offset, l.record+1); err != nil {
					return err
				}
			case b == '\r':
				l.state = stCR
			case b == '"':
				return l.fail(ErrBareQuote, KindBareQuote, offset)
			default:
				if err := l.add(b, offset); err != nil {
					return err
				}
			}
		case stQuoted:
			if b == '"' {
				l.state = stQuoteEnd
			} else if err := l.add(b, offset); err != nil {
				return err
			}
		case stCR:
			if b != '\n' {
				return l.fail(ErrBareCR, KindBareCR, offset-1)
			}
			if l.fields == 0 && !l.started {
				l.record++
				l.state = stStart
			} else if err := l.endRecord(offset-1, l.record+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Lexer) Close() error {
	if l.err != nil {
		return l.err
	}
	switch l.state {
	case stQuoted:
		return l.fail(ErrUnclosed, KindUnclosed, l.pos)
	case stCR:
		return l.fail(ErrBareCR, KindBareCR, l.pos-1)
	case stUnquoted, stQuoteEnd, stStart:
		if l.started || l.fields > 0 {
			return l.endRecord(l.pos, l.record)
		}
	}
	return nil
}
