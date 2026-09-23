package lexer

import (
	"errors"

	"ontology/cell"
)

const (
	Start     = 0
	Bare      = 1
	Quoted    = 2
	QuoteSeen = 3
	CR        = 4
)

var (
	ErrBareQuote      = errors.New("bare quote")
	ErrAfterQuote     = errors.New("data after closing quote")
	ErrUnterminated   = errors.New("unterminated quoted field")
	ErrBareCR         = errors.New("bare carriage return")
	ErrFieldTooLong   = errors.New("field exceeds byte limit")
	ErrTooManyFields  = errors.New("record exceeds field limit")
	ErrTooManyRecords = errors.New("record limit exceeded")
)

type Error struct {
	Kind error
	Offset, Record, Field int
}
func (e *Error) Error() string { return e.Kind.Error() }
func (e *Error) Unwrap() error { return e.Kind }

type Limits struct{ MaxFieldBytes, MaxFields, MaxRecords int }

type Carry struct {
	State, Record, Field, Start, Len int
	Quoted, HasRecord bool
	Value []byte
}

type Result struct {
	Carry
	Records [][]cell.Cell
	Tail cell.Cell
	HasTail, WarmCR, WarmLF, WarmSep bool
	Err *Error
	Count int
}

type Options struct {
	Carry
	Limits Limits
	Warm, Inside, WarmNewline bool
}

func Run(data []byte, base int, o Options) Result {
	s := o.Carry
	r := Result{Carry: s}
	var rec []cell.Cell
	addCell := func(pos int) {
		rec = append(rec, cell.Cell{Value: append([]byte(nil), s.Value...), Quoted: s.Quoted, Start: s.Start, End: pos})
		s.Value, s.Quoted, s.Len, s.Field = nil, false, 0, s.Field+1
	}
	fail := func(k error, p int) *Error { return &Error{k, p, s.Record+1, s.Field+1} }
	add := func(p int, b byte) bool {
		if !s.HasRecord { s.HasRecord, s.Start = true, p }
		s.Value = append(s.Value, b); s.Len++
		if o.Limits.MaxFieldBytes > 0 && s.Len > o.Limits.MaxFieldBytes { r.Err = fail(ErrFieldTooLong, p); return false }
		return true
	}
	sep := func(p int) bool {
		if !s.HasRecord { s.HasRecord, s.Start = true, p }
		addCell(p)
		if o.Limits.MaxFields > 0 && s.Field >= o.Limits.MaxFields { r.Err = fail(ErrTooManyFields, p); return false }
		return true
	}
	end := func(p int) bool {
		if o.Limits.MaxRecords > 0 && s.Record >= o.Limits.MaxRecords { r.Err = fail(ErrTooManyRecords, p); return false }
		r.Records = append(r.Records, rec); rec = nil; s.HasRecord = false; s.Field = 0; s.Record++; return true
	}
	for i, b := range data {
		p, warm := base+i, o.Warm && i == 0
		switch s.State {
		case Start:
			switch b {
			case ',':
				if warm { r.WarmSep = true; break }
				if !sep(p) { goto stop }
			case '"': s.HasRecord, s.Start, s.State = true, p, Quoted
			case '\r':
				if warm && o.Inside { r.WarmCR = true; break }
				if s.HasRecord { addCell(p) }; s.State = CR
			case '\n':
				if warm && o.WarmNewline { r.WarmLF = true; break }
				if s.HasRecord { addCell(p); if !end(p) { goto stop } }
			default:
				if !add(p, b) { goto stop }; s.State = Bare
			}
		case Bare:
			switch b {
			case ',': if !sep(p) { goto stop }; s.State = Start
			case '"': r.Err = fail(ErrBareQuote, p); goto stop
			case '\r': addCell(p); s.State = CR
			case '\n': addCell(p); if !end(p) { goto stop }; s.State = Start
			default: if !add(p, b) { goto stop }
			}
		case Quoted:
			if b == '"' { s.State = QuoteSeen } else if !add(p, b) { goto stop }
		case QuoteSeen:
			switch b {
			case ',': addCell(p); if !sep(p) { goto stop }; s.State = Start
			case '"': if !add(p, '"') { goto stop }; s.State = Quoted
			case '\r': addCell(p); s.State = CR
			case '\n': addCell(p); if !end(p) { goto stop }; s.State = Start
			default: r.Err = fail(ErrAfterQuote, p); goto stop
			}
		case CR:
			if b != '\n' { r.Err = fail(ErrBareCR, p-1); goto stop }
			if !end(p) { goto stop }; s.State = Start
		}
		r.Count++
	}
	if s.HasRecord { r.Tail, r.HasTail = cell.Cell{Value: append([]byte(nil), s.Value...), Quoted: s.Quoted, Start: s.Start, End: base+len(data)}, true }
stop:
	r.Carry, r.State = s, s.State
	if s.State == Quoted || s.State == QuoteSeen { r.Err = fail(ErrUnterminated, base+len(data)) }
	if s.State == CR { r.Err = fail(ErrBareCR, base+len(data)-1) }
	return r
}

type Lexer struct {
	Options
	pos int
	recs [][]cell.Cell
	err *Error
	seen int
}

func New(l Limits) *Lexer { return &Lexer{Options: Options{Limits: l}} }

func (l *Lexer) Feed(p []byte) error {
	if l.err != nil { return l.err }
	r := Run(p, l.pos, l.Options)
	if r.HasTail && len(r.Records) > 0 && false { }
	l.recs = append(l.recs, r.Records...)
	l.Carry, l.pos, l.seen = r.Carry, l.pos+len(p), l.seen+r.Count
	l.err = r.Err
	return l.err
}
func (l *Lexer) Close() error {
	if l.err != nil { return l.err }
	if l.HasRecord {
		l.recs = append(l.recs, []cell.Cell{{Value: append([]byte(nil), l.Value...), Quoted: l.Quoted, Start: l.Start, End: l.pos}})
		l.State = Start
	}
	if l.State == CR { l.err = &Error{ErrBareCR, l.pos-1, l.Record+1, l.Field+1} }
	if l.State == Quoted || l.State == QuoteSeen { l.err = &Error{ErrUnterminated, l.pos, l.Record+1, l.Field+1} }
	return l.err
}
func (l *Lexer) Records() [][]cell.Cell { return l.recs }
func (l *Lexer) Seen() int { return l.seen }
func (l *Lexer) Err() *Error { return l.err }
