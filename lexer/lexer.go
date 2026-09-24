package lexer

import (
	"errors"
	"fmt"
	"strings"

	"ontology/cell"
)

type Kind int

const (
	BareQuote Kind = iota + 1
	QuoteAfterClose
	UnclosedQuote
	LoneCR
	FieldTooLong
	TooManyFields
	TooManyRecords
	TerminalUse
)

var (
	ErrBareQuote       = errors.New("bare quote in unquoted field")
	ErrQuoteAfterClose = errors.New("data after closing quote")
	ErrUnclosedQuote   = errors.New("unclosed quoted field")
	ErrLoneCR          = errors.New("bare carriage return")
	ErrFieldTooLong    = errors.New("field too long")
	ErrTooManyFields   = errors.New("too many fields")
	ErrTooManyRecords  = errors.New("too many records")
	ErrTerminal        = errors.New("parser is in terminal state")
)

type Error struct {
	Kind                  Kind
	Offset, Record, Field int
	err                   error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v at byte %d, record %d, field %d", e.err, e.Offset, e.Record, e.Field)
}
func (e *Error) Unwrap() error { return e.err }

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }
type Handler interface {
	Field(cell.Cell)
	Record(endOffset int)
}
type State int

const (
	Start State = iota
	Data
	Quoted
	QuoteClose
	CRPending
)

type Parser struct {
	h                            Handler
	lim                          Limits
	base, processed              int
	rec, field                   int
	state                        State
	live, quoted                 bool
	start, rawEnd, quoteAt, crAt int
	value                        strings.Builder
	terminal                     *Error
}

func New(h Handler, l Limits) *Parser {
	return &Parser{h: h, lim: l, rec: 1, field: 1, start: -1}
}

func NewFragment(h Handler, base, rec, field int, st State, start int, lim Limits) *Parser {
	p := New(h, lim)
	p.base, p.rec, p.field, p.state, p.start = base, rec, field, st, start
	p.live = st != Start || start == base
	p.quoted = st == Quoted || st == QuoteClose
	p.rawEnd = base
	if p.quoted {
		p.quoteAt = start
	}
	return p
}

func (p *Parser) ProcessedBytes() int { return p.processed }
func (p *Parser) State() State        { return p.state }
func (p *Parser) RecordNo() int       { return p.rec }
func (p *Parser) FieldNo() int        { return p.field }
func (p *Parser) Live() bool          { return p.live }
func (p *Parser) FieldStart() int     { return p.start }
func (p *Parser) QuoteStart() int     { return p.quoteAt }
func (p *Parser) Err() *Error         { return p.terminal }

func (p *Parser) Pending() (cell.Cell, bool) {
	if !p.live || p.state == CRPending {
		return cell.Cell{}, false
	}
	return cell.Cell{Value: p.value.String(), Quoted: p.quoted, Start: p.start, End: p.rawEnd}, true
}

func (p *Parser) fail(k, off int, err error) error {
	p.terminal = &Error{Kind: Kind(k), Offset: off, Record: p.rec, Field: p.field, err: err}
	return p.terminal
}

func (p *Parser) begin(off int) error {
	if p.live {
		return nil
	}
	if p.lim.MaxRecords > 0 && p.rec > p.lim.MaxRecords {
		return p.fail(int(TooManyRecords), off, ErrTooManyRecords)
	}
	p.live, p.start, p.rawEnd = true, off, off
	return nil
}

func (p *Parser) add(b byte, off int) error {
	if p.lim.MaxFieldBytes > 0 && p.value.Len() >= p.lim.MaxFieldBytes {
		return p.fail(int(FieldTooLong), off, ErrFieldTooLong)
	}
	p.value.WriteByte(b)
	p.rawEnd = off + 1
	return nil
}

func (p *Parser) field() {
	if p.h != nil {
		p.h.Field(cell.Cell{Value: p.value.String(), Quoted: p.quoted, Start: p.start, End: p.rawEnd})
	}
	p.field++
}

func (p *Parser) fields(off int) error {
	if p.lim.MaxFields > 0 && p.field > p.lim.MaxFields {
		return p.fail(int(TooManyFields), off, ErrTooManyFields)
	}
	return nil
}

func (p *Parser) next(off int) {
	p.value, p.quoted, p.live, p.start, p.rawEnd, p.state = strings.Builder{}, false, true, off, off, Start
}

func (p *Parser) record(end int) {
	if p.h != nil {
		p.h.Record(end)
	}
	p.value, p.quoted, p.live = strings.Builder{}, false, false
	p.state, p.field, p.start, p.rawEnd = Start, 1, -1, end
	p.rec++
}

func (p *Parser) Feed(data []byte) error {
	if p.terminal != nil {
		return p.terminal
	}
	for i := 0; i < len(data); i++ {
		b, pos := data[i], p.base+i
		p.processed++
		if p.state == Start {
			if err := p.begin(pos); err != nil {
				return err
			}
			switch b {
			case ',':
				p.field()
				if err := p.fields(pos); err != nil {
					return err
				}
				p.next(pos + 1)
			case '"':
				p.state, p.quoted, p.quoteAt, p.rawEnd = Quoted, true, pos, pos+1
			case '\r':
				p.field()
				p.state, p.crAt, p.quoted = CRPending, pos, false
			case '\n':
				p.field()
				p.record(pos + 1)
			default:
				if err := p.add(b, pos); err != nil {
					return err
				}
				p.state = Data
			}
			continue
		}
		switch p.state {
		case Data:
			switch b {
			case ',':
				p.field()
				if err := p.fields(pos); err != nil {
					return err
				}
				p.next(pos + 1)
			case '"':
				return p.fail(int(BareQuote), pos, ErrBareQuote)
			case '\r':
				p.field()
				p.state, p.crAt = CRPending, pos
			case '\n':
				p.field()
				p.record(pos + 1)
			default:
				if err := p.add(b, pos); err != nil {
					return err
				}
			}
		case Quoted:
			if b == '"' {
				p.state, p.rawEnd = QuoteClose, pos+1
			} else if err := p.add(b, pos); err != nil {
				return err
			}
		case QuoteClose:
			switch b {
			case '"':
				p.state = Quoted
				if err := p.add('"', pos); err != nil {
					return err
				}
			case ',':
				p.field()
				if err := p.fields(pos); err != nil {
					return err
				}
				p.next(pos + 1)
			case '\r':
				p.field()
				p.state, p.crAt, p.quoted = CRPending, pos, false
			case '\n':
				p.field()
				p.record(pos + 1)
			default:
				return p.fail(int(QuoteAfterClose), pos, ErrQuoteAfterClose)
			}
		case CRPending:
			if b != '\n' {
				return p.fail(int(LoneCR), p.crAt, ErrLoneCR)
			}
			p.record(pos + 1)
		}
	}
	p.base += len(data)
	return nil
}

func (p *Parser) End() error {
	switch p.state {
	case Quoted:
		return p.fail(int(UnclosedQuote), p.quoteAt, ErrUnclosedQuote)
	case CRPending:
		return p.fail(int(LoneCR), p.crAt, ErrLoneCR)
	}
	return nil
}

func (p *Parser) Close() error {
	if p.terminal != nil {
		return p.terminal
	}
	if err := p.End(); err != nil {
		return err
	}
	if p.live {
		p.field()
		p.record(p.rawEnd)
	}
	return nil
}
