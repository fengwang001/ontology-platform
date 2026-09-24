// Package lexer is a resumable byte-at-a-time RFC4180-dialect state machine.
package lexer

import (
	"fmt"

	"ontology/cell"
)

// Kind enumerates every distinguishable failure.
type Kind int

// Error carries the global byte offset (from 0) and 1-based record/field.
type Error struct {
	Kind                  Kind
	Offset, Record, Field int
}

// State: FS field start, B bare, Q quoted, QP quote seen, CR pending.
type State uint8

// Item is one emitted event; exactly one of Field/Line/Err is meaningful.
type Item struct {
	Field cell.Cell
	Line  int // >=0: absolute offset of the record terminator
	Err   *Error
}

// L is one streaming state machine. Not safe for concurrent use.
type L struct {
	maxField     int
	base, pos    int
	state        State
	start, crPos int
	crOpen       bool
	quoted       bool
	fbytes       int
	val          []byte
	n            int // unexported: bytes processed
	term         *Error
	out          func(Item)
}

func (e *Error) Error() string {
	return fmt.Sprintf("csv: kind=%d at byte %d (record %d field %d)", e.Kind, e.Offset, e.Record, e.Field)
}

// New builds a machine starting at absolute offset base; maxField<=0 unlimited.
func New(base, maxField int, out func(Item)) *L { return &L{base: base, maxField: maxField, out: out} }

// StartAt builds a machine with an open field beginning at start in state s.
func StartAt(base, maxField, start int, s State, quoted bool, val []byte, out func(Item)) *L {
	l := New(base, maxField, out)
	l.state, l.quoted, l.start = s, quoted, start
	l.val, l.fbytes = append([]byte(nil), val...), len(val)
	return l
}

// Count reports bytes processed; State reports the current state.
func (l *L) Count() int   { return l.n }
func (l *L) State() State { return l.state }

func (l *L) fail(k, off int) bool {
	l.term = &Error{Kind: Kind(k), Offset: off}
	l.out(Item{Err: l.term})
	return false
}

func (l *L) field(end int) {
	l.out(Item{Field: cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.start, End: end}})
	l.val, l.quoted, l.fbytes = nil, false, 0
}

func (l *L) add(b byte) bool {
	l.val, l.fbytes = append(l.val, b), l.fbytes+1
	if l.maxField > 0 && l.fbytes > l.maxField {
		return l.fail(int(ErrFieldTooLong), l.pos)
	}
	return true
}

// Close finishes the stream; a dangling quoted field or lone CR is an error.
func (l *L) Close() error {
	if l.term != nil {
		return l.term
	}
	switch l.state {
	case Q:
		l.term = &Error{Kind: ErrUnclosedQuote, Offset: l.start}
	case CR:
		if l.crOpen {
			l.field(l.crPos)
		}
	case B, QP:
		l.field(l.base)
	}
	if l.term != nil {
		l.out(Item{Err: l.term})
	}
	if l.term != nil {
		return l.term
	}
	return nil
}

// Result is the collected output of one Run.
type Result struct {
	Items []Item
	State State
	Err   *Error
	N     int
}

// Run parses p (finalizing if final) starting with an open field in state s.
func Run(base, maxField, start int, s State, quoted bool, val []byte, p []byte, final bool) Result {
	var r Result
	l := StartAt(base, maxField, start, s, quoted, val, func(it Item) {
		if it.Err != nil {
			r.Err = it.Err
		} else {
			r.Items = append(r.Items, it)
		}
	})
	l.Feed(p)
	if final && r.Err == nil {
		l.Close()
	}
	r.State, r.N = l.state, l.n
	return r
}
