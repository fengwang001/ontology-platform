package lexer

import "ontology/cell"

// Sink receives lexical events with global byte offsets.
type Sink interface {
	FieldStart(off int64, quoted bool)
	FieldData(p []byte)
	FieldEnd(off int64)
	RecordEnd(off int64)
}

// Entry is the incoming state for a segment parse, see DESIGN.md §2.
type Entry int

const (
	EntryStart  Entry = 0 // FStart
	EntryBare   Entry = 1 // FBare, open field carried from previous segment
	EntryQuote  Entry = 2 // FQuote; replay of p[-1] must be suppressed by caller
	EntryQuoteQ Entry = 3 // FQuoteQ; replay byte emitted as closing quote
	EntryCR     Entry = 4 // FCR
)

// End is the state after a segment parse.
type End int

const (
	EndStart End = iota
	EndBare
	EndQuote
	EndQuoteQ
	EndCR
)

// SegResult is the outcome of parsing one segment.
type SegResult struct {
	End    End
	Err    *cell.PosError
	Count  int64 // bytes processed by the machine
}

// ParseSegment runs one pass over p under entry state. replay is the byte
// before the segment (EntryQuote/EntryQuoteQ); it is processed but never
// appended as field content. base is added to reported offsets.
func ParseSegment(p []byte, entry Entry, replay byte, base int64,
	maxFieldBytes int64, sink Sink) SegResult {
	return SegResult{}
}

// Lexer is a resumable byte-at-a-time streaming lexer. Not goroutine-safe.
type Lexer struct {
	bytesProcessed int64
}

// New builds a Lexer. maxFieldBytes <= 0 means unlimited.
func New(sink Sink, maxFieldBytes int64) *Lexer { return &Lexer{} }

// Feed appends input; it may be called any number of times.
func (l *Lexer) Feed(p []byte) error { return nil }

// Close finalizes the stream; the terminal error is sticky.
func (l *Lexer) Close() error { return nil }

// BytesProcessed reports the exact number of bytes the machine processed.
func (l *Lexer) BytesProcessed() int64 { return l.bytesProcessed }
