package lzw

import (
	"io"
	"sync"
)

const (
	clearCode = 256
	endCode   = 257
	firstFree = 258
	minWidth  = 9
	maxWidth  = 12
	tableSize = 1 << maxWidth // 4096
)

// Encoder is a streaming GIF-style LZW encoder. It is safe for concurrent
// use; concurrent calls behave as if executed in some serial order.
//
// Code width starts at 9 bits and grows to at most 12. Codes are packed into
// bytes least-significant-bit first. The leading code emitted is always the
// clear code 256.
type Encoder struct {
	mu      sync.Mutex
	w       io.Writer
	width   int
	next    int            // next unused dictionary code
	wcur    string         // current matched prefix, empty before the first byte
	table   map[string]int // strings of length >= 2; literals are always known
	acc     uint32         // pending packed bits
	nbits   int            // number of valid bits in acc
	pending []byte         // completed bytes waiting to be written
	err     error          // sticky failure
	closed  bool
}

// NewEncoder creates an encoder that writes the packed byte stream to w. The
// clear code is emitted eagerly, so even an immediately Closed encoder yields
// a valid empty-input stream.
func NewEncoder(w io.Writer) *Encoder {
	e := &Encoder{
		w:     w,
		width: minWidth,
		next:  firstFree,
		table: make(map[string]int),
	}
	e.putCode(clearCode)
	e.flushBytes()
	return e
}

// Write feeds input bytes. It may be called any number of times; the packed
// output depends only on the concatenated input, never on call boundaries.
// Write after Close fails with ErrWriteAfterClose and changes no state.
func (e *Encoder) Write(p []byte) (int, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return 0, ErrWriteAfterClose
	}
	if e.err != nil {
		return 0, e.err
	}
	for _, c := range p {
		cs := string([]byte{c})
		wc := e.wcur + cs
		if len(e.wcur) == 0 {
			e.wcur = cs
			continue
		}
		if _, ok := e.table[wc]; ok {
			e.wcur = wc
			continue
		}
		e.putCode(e.lookup(e.wcur))
		entry := e.next
		e.table[wc] = entry
		e.next++
		e.afterAdd(entry)
		e.wcur = cs
	}
	if err := e.flushBytes(); err != nil {
		e.err = err
		return 0, err
	}
	return len(p), nil
}

// Close emits the pending match, performs the post-close width bump (the next
// free code is counted as occupied even though nothing will ever reference
// it), emits the end-of-information code, and pads to a byte boundary with
// zero bits. Close is idempotent: a second Close returns the same error and
// emits nothing more.
func (e *Encoder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return e.err
	}
	e.closed = true
	if e.err != nil {
		return e.err
	}
	if len(e.wcur) > 0 {
		e.putCode(e.lookup(e.wcur))
	}
	// Count the next free number as occupied; it may trigger a width bump
	// that only affects the end-of-information code.
	phantom := e.next
	e.next++
	if phantom == 1<<e.width && e.width < maxWidth {
		e.width++
	}
	e.putCode(endCode)
	for e.nbits >= 8 {
		e.outByte(byte(e.acc))
		e.acc >>= 8
		e.nbits -= 8
	}
	if e.nbits > 0 {
		e.outByte(byte(e.acc))
		e.acc = 0
		e.nbits = 0
	}
	if err := e.flushBytes(); err != nil {
		e.err = err
		return err
	}
	return nil
}

// lookup maps a current match to its code.
func (e *Encoder) lookup(s string) int {
	if len(s) == 1 {
		return int(s[0])
	}
	code, ok := e.table[s]
	if !ok {
		panic("lzw: encoder emitted an unmapped string")
	}
	return code
}

// afterAdd applies the width-bump and full-table rules that follow every
// real dictionary insertion.
func (e *Encoder) afterAdd(entry int) {
	if entry == 1<<e.width && e.width < maxWidth {
		e.width++
		return
	}
	if entry == tableSize-1 { // code 4095: table now holds 0..4095
		e.putCode(clearCode)
		e.resetTable()
	}
}

// resetTable restores the initial dictionary (0..257), width 9; the caller
// keeps w as the current match.
func (e *Encoder) resetTable() {
	e.table = make(map[string]int)
	e.next = firstFree
	e.width = minWidth
}

// putCode appends one code to the LSB-first bit buffer.
func (e *Encoder) putCode(code int) {
	for e.nbits > 32-maxWidth {
		e.outByte(byte(e.acc))
		e.acc >>= 8
		e.nbits -= 8
	}
	e.acc |= uint32(code) << e.nbits
	e.nbits += e.width
}

// outByte buffers one output byte (kept off the write hot path until a
// boundary so partial bytes survive across Write calls).
func (e *Encoder) outByte(b byte) {
	e.pending = append(e.pending, b)
}

// flushBytes pushes all completed bytes to the underlying writer.
func (e *Encoder) flushBytes() error {
	for e.nbits >= 8 {
		e.outByte(byte(e.acc))
		e.acc >>= 8
		e.nbits -= 8
	}
	if len(e.pending) == 0 {
		return nil
	}
	buf := e.pending
	e.pending = nil
	if _, err := e.w.Write(buf); err != nil {
		return err
	}
	return nil
}
