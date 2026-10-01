package lzw

import (
	"io"
	"sync"
)

// Decoder is a streaming GIF-style LZW decoder. It is safe for concurrent
// use. After any decoding error it enters a sticky failure state: later
// calls return the same reason and never mutate state.
//
// Width growth is derived from the encoder rules. The encoder inserts one
// entry after each emitted match starting with the first data code; the
// decoder is exactly one insertion behind, inserting its entry for emitted
// code k only after reading code k+1. Thus:
//
//   - The first data code after a clear inserts nothing.
//   - After every data code except the first, the decoder inserts one entry,
//     staying exactly one insertion behind the encoder. Before reading the
//     next code, if the next free number equals 2^width, the encoder has
//     already sent that code (which may equal 2^width itself: the KwKwK
//     self-reference) with width+1, so the width is bumped then.
type Decoder struct {
	mu sync.Mutex
	w  io.Writer

	width   int      // width used to read the next code
	next    int      // next free dictionary number
	first   bool     // next data code is the first after a (re)start
	prev    []byte   // previous output string (nil at start / after clear)
	table   [][]byte // table[258..next-1]
	codeIdx int      // ordinal of the last consumed code, clear included
	eoi     bool     // end-of-information seen

	buf    []byte // bytes not yet consumed
	pos    int    // read position inside buf
	bitOff int    // bit offset inside buf[pos] (LSB-first)

	err error // sticky failure
}

// NewDecoder creates a decoder writing recovered bytes to w.
func NewDecoder(w io.Writer) *Decoder {
	return &Decoder{
		w:     w,
		width: minWidth,
		next:  firstFree,
		first: true,
		table: make([][]byte, tableSize),
	}
}

// Write feeds compressed bytes. A failure is sticky: the recorded reason is
// returned by this and every following call (including Close).
func (d *Decoder) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return 0, d.err
	}
	if d.eoi {
		d.fail(&CodeError{CodeIndex: d.codeIdx, Err: ErrTrailingData})
		return 0, d.err
	}
	d.buf = append(d.buf, p...)
	for {
		if !d.first && d.width < maxWidth && d.next == 1<<d.width {
			d.width++
		}
		if !d.haveCode() {
			break
		}
		code := d.readCode()
		d.codeIdx++
		switch {
		case code == clearCode:
			d.reset()
			continue
		case code == endCode:
			d.eoi = true
			if err := d.checkPadding(); err != nil {
				d.fail(err)
				return 0, d.err
			}
			d.buf = nil
			d.pos = 0
			d.bitOff = 0
			return len(p), nil
		}
		if d.codeIdx == 1 {
			d.fail(&CodeError{CodeIndex: d.codeIdx, Err: ErrFirstCodeNotClear})
			return 0, d.err
		}
		if d.first {
			if code >= clearCode {
				d.fail(&CodeError{CodeIndex: d.codeIdx, Err: ErrFirstAfterClear})
				return 0, d.err
			}
			d.first = false
			d.emitLiteral(byte(code))
			if d.err != nil {
				return 0, d.err
			}
		} else {
			var out, entry []byte
			switch {
			case code < clearCode:
				out = []byte{byte(code)}
				entry = append(append([]byte(nil), d.prev...), out[0])
			case code < d.next:
				out = d.table[code]
				entry = append(append([]byte(nil), d.prev...), out[0])
			case code == d.next:
				// KwKwK: the code names the entry that is being added now.
				out = append(append([]byte(nil), d.prev...), d.prev[0])
				entry = out
			default:
				d.fail(&CodeError{CodeIndex: d.codeIdx, Err: ErrInvalidCode})
				return 0, d.err
			}
			d.emit(out)
			if d.err != nil {
				return 0, d.err
			}
			d.addEntry(entry)
		}
	}
	if d.pos > 0 {
		d.buf = append([]byte(nil), d.buf[d.pos:]...)
		d.pos = 0
	}
	return len(p), nil
}

// Close reports ErrTruncated unless a well-formed end-of-information code was
// already consumed.
func (d *Decoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	if d.eoi {
		return nil
	}
	d.fail(&CodeError{CodeIndex: d.codeIdx + 1, Err: ErrTruncated})
	return d.err
}

func (d *Decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

func (d *Decoder) reset() {
	d.width = minWidth
	d.next = firstFree
	d.prev = nil
	d.first = true
	d.table = make([][]byte, tableSize)
}

func (d *Decoder) emitLiteral(b byte) {
	out := []byte{b}
	d.emit(out)
}

func (d *Decoder) emit(out []byte) {
	if _, err := d.w.Write(out); err != nil {
		d.fail(&writerError{err})
		return
	}
	d.prev = out
}

// addEntry stores s at the next free number. The width bump is evaluated at
// the top of the read loop via next == 2^width.
func (d *Decoder) addEntry(s []byte) {
	d.table[d.next] = s
	d.next++
}

// haveCode reports whether width bits remain in the buffered input.
func (d *Decoder) haveCode() bool {
	avail := len(d.buf[d.pos:])*8 - d.bitOff
	return avail >= d.width
}

// readCode consumes width bits LSB-first; haveCode must hold.
func (d *Decoder) readCode() int {
	code := 0
	for i := 0; i < d.width; i++ {
		if d.buf[d.pos]&(1<<d.bitOff) != 0 {
			code |= 1 << i
		}
		d.bitOff++
		if d.bitOff == 8 {
			d.bitOff = 0
			d.pos++
		}
	}
	return code
}

// checkPadding verifies the unused bits of the current byte are zero and that
// no extra bytes follow the end-of-information code.
func (d *Decoder) checkPadding() error {
	if d.bitOff != 0 {
		if d.buf[d.pos]&(0xff<<d.bitOff) != 0 {
			return &CodeError{CodeIndex: d.codeIdx, Err: ErrPaddingNonZero}
		}
		d.pos++
	}
	if d.pos < len(d.buf) {
		return &CodeError{CodeIndex: d.codeIdx, Err: ErrTrailingData}
	}
	return nil
}

// writerError preserves failures of the underlying output writer.
type writerError struct{ err error }

func (e *writerError) Error() string { return e.err.Error() }
func (e *writerError) Unwrap() error { return e.err }
