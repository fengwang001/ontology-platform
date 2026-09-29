// Package lexer is a resumable byte-at-a-time CSV state machine.
package lexer

import "errors"

// State of the machine between byte chunks.
type State uint8

const (
	StField    State = iota // start of a field
	StBare                  // inside unquoted field
	StQuoted                // inside quoted field
	StAfter                 // right after closing quote
	StBareCR                // CR after unquoted content / at field start
	StQuoteCR               // CR after closing quote
)

// Limits configure resource caps; zero means unlimited.
type Limits struct {
	MaxFieldBytes int
}

// EventKind enumerates frame events.
type EventKind uint8

const (
	EvFieldStart EventKind = iota
	EvAdd
	EvQuote
	EvFieldEnd
	EvEOL
	EvError
)

// Event is one state-machine output. Pos is a global byte offset.
type Event struct {
	Kind EventKind
	Pos  int
	End  int
	Qu   bool
	B    byte
	Err  error
}

// Seed resumes a frame at a boundary. QuoteAt is the opening quote offset for
// a StQuoted seed; VLen is the decoded field length accumulated before it.
type Seed struct {
	St      State
	Base    int
	QuoteAt int
	VLen    int
}

// Sentinel errors; all parse failures are one of these.
var (
	ErrQuoteInBare       = errors.New("lexer: bare quote in unquoted field")
	ErrGarbageAfterQuote = errors.New("lexer: unexpected character after closing quote")
	ErrUnterminatedQuote = errors.New("lexer: unterminated quoted field")
	ErrBareCR            = errors.New("lexer: bare carriage return not followed by newline")
	ErrFieldTooLong      = errors.New("lexer: field exceeds MaxFieldBytes")
)

// Frame is one independent state-machine execution.
type Frame struct {
	Seed
	Lim Limits

	processed int64
	// mutable execution state
	st        State
	open      bool // a field start event has been emitted and no end yet
	nFields   int  // completed fields in the current record
	vlen      int  // decoded length of the open field
	crPos     int  // position of the CR currently pending
	inited    bool
}

func (f *Frame) init() {
	if f.inited {
		return
	}
	f.inited = true
	f.st = f.Seed.St
	f.vlen = f.Seed.VLen
	if f.st == StQuoted {
		f.open = true
	}
}

func (f *Frame) fail(emit func(Event), pos int, err error) error {
	emit(Event{Kind: EvError, Pos: pos, Err: err})
	return err
}

func (f *Frame) startField(emit func(Event), pos int, quoted bool) {
	f.open = true
	emit(Event{Kind: EvFieldStart, Pos: pos, Qu: quoted})
}

func (f *Frame) add(emit func(Event), pos int, b byte) error {
	if f.Lim.MaxFieldBytes > 0 && f.vlen+1 > f.Lim.MaxFieldBytes {
		return f.fail(emit, pos, ErrFieldTooLong)
	}
	f.vlen++
	emit(Event{Kind: EvAdd, Pos: pos, B: b})
	return nil
}

func (f *Frame) endField(emit func(Event), pos int) {
	if f.open {
		emit(Event{Kind: EvFieldEnd, Pos: pos})
		f.open = false
	}
	f.nFields++
	f.vlen = 0
}

func (f *Frame) eol(emit func(Event), pos int) {
	emit(Event{Kind: EvEOL, Pos: pos})
	f.nFields = 0
}

// blankField emits an empty unquoted field spanning [pos,pos).
func (f *Frame) blankField(emit func(Event), pos int) {
	f.startField(emit, pos, false)
	f.endField(emit, pos)
}

// Run feeds bytes, emitting events until p is consumed or an error occurs.
func (f *Frame) Run(p []byte, emit func(Event)) error {
	f.init()
	for i, b := range p {
		pos := f.Base + i
		f.processed++
		switch f.st {
		case StField:
			switch {
			case b == ',':
				f.blankField(emit, pos)
			case b == '"':
				f.startField(emit, pos, true)
				f.QuoteAt = pos
				f.st = StQuoted
			case b == '\r':
				f.crPos = pos
				f.st = StBareCR
			case b == '\n':
				f.eol(emit, pos) // empty line: skipped by the assembler
			default:
				f.startField(emit, pos, false)
				if err := f.add(emit, pos, b); err != nil {
					return err
				}
				f.st = StBare
			}
		case StBare:
			switch {
			case b == ',':
				f.endField(emit, pos)
				f.st = StField
			case b == '"':
				return f.fail(emit, pos, ErrQuoteInBare)
			case b == '\r':
				f.crPos = pos
				f.endField(emit, pos)
				f.st = StBareCR
			case b == '\n':
				f.endField(emit, pos)
				f.eol(emit, pos)
				f.st = StField
			default:
				if err := f.add(emit, pos, b); err != nil {
					return err
				}
			}
		case StQuoted:
			switch {
			case b == '"':
				f.st = StAfter
			case b == '\r' || b == '\n':
				if err := f.add(emit, pos, b); err != nil {
					return err
				}
			default:
				if err := f.add(emit, pos, b); err != nil {
					return err
				}
			}
		case StAfter:
			switch {
			case b == ',':
				f.endField(emit, pos)
				f.st = StField
			case b == '"':
				if err := f.add(emit, pos, '"'); err != nil {
					return err
				}
				f.st = StQuoted
			case b == '\r':
				f.crPos = pos
				f.endField(emit, pos)
				f.st = StQuoteCR
			case b == '\n':
				f.endField(emit, pos)
				f.eol(emit, pos)
				f.st = StField
			default:
				return f.fail(emit, pos, ErrGarbageAfterQuote)
			}
		case StBareCR, StQuoteCR:
			if b == '\n' {
				f.eol(emit, pos)
				f.st = StField
			} else {
				return f.fail(emit, f.crPos, ErrBareCR)
			}
		}
	}
	return nil
}

// Finish closes the stream at global offset endPos.
func (f *Frame) Finish(endPos int, emit func(Event)) error {
	f.init()
	switch f.st {
	case StQuoted:
		return f.fail(emit, f.QuoteAt, ErrUnterminatedQuote)
	case StBareCR, StQuoteCR:
		return f.fail(emit, f.crPos, ErrBareCR)
	case StBare, StAfter:
		f.endField(emit, endPos)
		f.eol(emit, endPos)
	case StField:
		if f.nFields > 0 { // record ending in a trailing comma
			f.blankField(emit, endPos)
			f.eol(emit, endPos)
		}
	}
	return nil
}

// Processed reports bytes handled by the state machine.
func (f *Frame) Processed() int64 { return f.processed }
