// Package aentry defines queue entries (element / watermark), completion
// sequencing and input validation. It depends on nothing else in the module.
package aentry

import (
	"errors"
	"strconv"
)

// Distinguishable sentinel errors for every rejectable failure.
var (
	ErrFull      = errors.New("capacity full")
	ErrUnknown   = errors.New("unknown or already completed id")
	ErrDuplicate = errors.New("empty or duplicate element id")
	ErrWatermark = errors.New("watermark not strictly increasing")
)

// Kind discriminates queue entries.
type Kind int

const (
	Element Kind = iota
	Watermark
)

// Entry is one queued item: an element awaiting its async result, or a
// watermark. Done/Seq are meaningful only for elements.
type Entry struct {
	Kind Kind
	ID   string // element id
	T    int64  // watermark time
	Done bool   // request completed
	Seq  uint64 // completion sequence number, 0 until done
	Out  bool   // already emitted (used by the naive reference)
}

// Elem builds an element entry.
func Elem(id string) Entry { return Entry{Kind: Element, ID: id} }

// WM builds a watermark entry.
func WM(t int64) Entry { return Entry{Kind: Watermark, T: t} }

func (e Entry) String() string {
	if e.Kind == Watermark {
		return "W" + strconv.FormatInt(e.T, 10)
	}
	return e.ID
}

// Sequencer hands out completion sequence numbers (1, 2, ...).
type Sequencer struct{ n uint64 }

// Next returns the next completion sequence number.
func (s *Sequencer) Next() uint64 { s.n++; return s.n }

// CheckID rejects an empty id or one already accepted (ErrDuplicate).
func CheckID(id string, exists func(string) bool) error {
	if id == "" || exists(id) {
		return ErrDuplicate
	}
	return nil
}

// CheckWatermark rejects t unless it strictly exceeds the last watermark.
func CheckWatermark(t, last int64, hasLast bool) error {
	if hasLast && t <= last {
		return ErrWatermark
	}
	return nil
}
