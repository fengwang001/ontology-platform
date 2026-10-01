// Package ontology implements a streaming tokenizer with character folding
// and deletion filters.
package ontology

import "sync"

// maxTokenLen is the inclusive maximum number of output bytes a reported
// token may contain. Tokens of exactly 64 bytes are kept; 65 and more are
// dropped but still occupy a position serial.
const maxTokenLen = 64

// ErrorKind enumerates the distinguishable rejection reasons of the
// tokenizer operations.
type ErrorKind int

const (
	// ErrNone is the zero value and denotes no error.
	ErrNone ErrorKind = iota
	// ErrClosed means the tokenizer has already been closed.
	ErrClosed
	// ErrInvalidUTF8 means the chunk, concatenated with any buffered
	// incomplete tail, contains a byte sequence that cannot be the
	// prefix of any legal UTF-8 encoding. The whole chunk is rejected.
	ErrInvalidUTF8
	// ErrTruncated means Close saw a buffered incomplete UTF-8 tail.
	// The stream is not closed and more bytes may be Fed.
	ErrTruncated
)

func (k ErrorKind) String() string {
	switch k {
	case ErrClosed:
		return "closed"
	case ErrInvalidUTF8:
		return "invalid utf-8"
	case ErrTruncated:
		return "truncated utf-8 tail"
	default:
		return "none"
	}
}

// FeedError describes a rejected Feed or Close operation.
type FeedError struct {
	Kind ErrorKind
}

func (e *FeedError) Error() string {
	return "tokenizer: " + e.Kind.String()
}

// Token is a maximal run of token bytes [a-z0-9] after per-character
// filtering. Positions are byte offsets in the original input stream.
type Token struct {
	Text  string
	Start int
	End   int
	Pos   int
}

// Stats summarizes a finalized stream.
type Stats struct {
	Tokens   int
	Dropped  int
	Consumed int
}

// Tokenizer turns a stream of arbitrary UTF-8 byte chunks into lower-cased
// alphanumeric tokens.
type Tokenizer struct {
	mu      sync.Mutex
	pending []byte // buffered incomplete UTF-8 tail from the previous Feed

	// Stream counters, in bytes of the original input. Only whole,
	// decoded source characters advance consumed.
	consumed int

	inToken bool
	tok     []byte // token bytes accumulated for the current token
	start   int    // start byte offset of the current token
	end     int    // running end, swallowing trailing deleted characters
	pos     int    // position serial assigned to the current token

	tokens  int // reported tokens (excludes dropped ones)
	dropped int
	closed  bool
	ready   []Token // tokens completed during the ongoing Feed
}

// NewTokenizer creates an empty tokenizer.
func NewTokenizer() *Tokenizer {
	return &Tokenizer{}
}

// Feed appends a byte chunk and returns all tokens that become complete
// during this chunk, in ascending position order.
//
// If the chunk, concatenated with the buffered incomplete tail, contains
// a byte sequence that can already be determined not to be a prefix of
// any legal UTF-8 encoding (overlong encodings, surrogate code points or
// code points above U+10FFFF included), the whole chunk is rejected and
// no state or statistic changes.
func (t *Tokenizer) Feed(chunk []byte) ([]Token, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, &FeedError{Kind: ErrClosed}
	}

	// Snapshot the mutable state so a rejected chunk leaves everything
	// untouched. pending is copied: the caller may reuse chunk memory,
	// and the snapshot must not alias it either.
	snap := state{
		pending:  append([]byte(nil), t.pending...),
		consumed: t.consumed,
		inToken:  t.inToken,
		tok:      append([]byte(nil), t.tok...),
		start:    t.start,
		end:      t.end,
		pos:      t.pos,
		tokens:   t.tokens,
		dropped:  t.dropped,
	}

	data := make([]byte, 0, len(snap.pending)+len(chunk))
	data = append(data, snap.pending...)
	data = append(data, chunk...)

	// Absolute stream offset of data[0]. pending bytes were deliberately
	// not counted in consumed yet, so data begins exactly at consumed.
	base := t.consumed
	t.ready = t.ready[:0]
	i := 0
	for i < len(data) {
		r, _, n, status := decodeOne(data[i:])
		switch status {
		case statusComplete:
			t.processChar(r, base+i, n)
			i += n
		case statusIncomplete:
			// data[i:] is one legal-but-incomplete prefix; keep it.
			t.pending = append(t.pending[:0], data[i:]...)
			t.consumed = base + i // pending bytes are not consumed yet
			return t.takeReady(), nil
		default:
			t.restore(snap)
			return nil, &FeedError{Kind: ErrInvalidUTF8}
		}
	}

	t.pending = t.pending[:0]
	t.consumed = base + len(data)
	return t.takeReady(), nil
}

// Close flushes the final token, if any.
func (t *Tokenizer) Close() ([]Token, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil, &FeedError{Kind: ErrClosed}
	}
	if len(t.pending) > 0 {
		return nil, &FeedError{Kind: ErrTruncated}
	}

	t.closed = true
	if t.inToken {
		out := t.finishCurrent() // appends to no ready list; return directly
		t.inToken = false
		return out, nil
	}
	return nil, nil
}

// Stats returns the final counters after Close.
func (t *Tokenizer) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	return Stats{Tokens: t.tokens, Dropped: t.dropped, Consumed: t.consumed}
}

// state is a restorable snapshot of the mutable tokenizer state.
type state struct {
	pending  []byte
	consumed int
	inToken  bool
	tok      []byte
	start    int
	end      int
	pos      int
	tokens   int
	dropped  int
}

func (t *Tokenizer) restore(s state) {
	t.ready = nil
	t.pending = append(t.pending[:0], s.pending...)
	t.consumed = s.consumed
	t.inToken = s.inToken
	t.tok = append(t.tok[:0], s.tok...)
	t.start = s.start
	t.end = s.end
	t.pos = s.pos
	t.tokens = s.tokens
	t.dropped = s.dropped
}

// processChar folds one fully decoded source character spanning
// [charStart, charStart+size) into the token stream.
func (t *Tokenizer) processChar(r rune, charStart, size int) {
	charEnd := charStart + size
	out := mapChar(r)

	if out == nil {
		// Deleted character: neither token byte nor separator, but a
		// run of deletions immediately after the current token's last
		// token byte extends its end offset.
		if t.inToken {
			t.end = charEnd
		}
		return
	}

	for _, b := range out {
		if isTokenByte(b) {
			if !t.inToken {
				t.inToken = true
				t.tok = t.tok[:0]
				t.start = charStart
				t.end = charEnd
				t.pos = t.tokens + t.dropped
			}
			t.tok = append(t.tok, b)
			t.end = charEnd
		} else {
			if t.inToken {
				t.ready = append(t.ready, t.finishCurrent()...)
				t.inToken = false
			}
		}
	}
}

// finishCurrent finalizes the current token, either reporting it or
// counting it as dropped, and returns it when reported.
func (t *Tokenizer) finishCurrent() []Token {
	if len(t.tok) > maxTokenLen {
		t.dropped++
		return nil
	}
	text := string(t.tok) // copy: must not alias the internal buffer
	t.tokens++
	return []Token{{Text: text, Start: t.start, End: t.end, Pos: t.pos}}
}

// takeReady returns the tokens completed in this Feed and clears the
// staging area. The returned slice never aliases internal buffers:
// finishCurrent allocates a fresh slice and a fresh string per token.
func (t *Tokenizer) takeReady() []Token {
	if len(t.ready) == 0 {
		return nil
	}
	out := t.ready
	t.ready = nil
	return out
}
