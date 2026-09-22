package pct

import "unicode/utf8"

// Normalize percent-normalizes a single URL component (path segment, query
// key or value). Escapes of unreserved characters are decoded to literals;
// every other escape is preserved with upper-case hex digits. Literal bytes
// are copied untouched.
//
// The logical (fully decoded) byte sequence is incrementally validated as
// UTF-8. On any error Normalize returns "", false and an *EscapeError; it
// never returns partially decoded output.
//
// The second result reports whether the normalized form differs byte-for-byte
// from the input.
func Normalize(s string) (string, bool, error) {
	out := make([]byte, 0, len(s))
	dec := newDecoder()

	for i := 0; i < len(s); {
		b := s[i]
		scans++
		if b != '%' {
			if err := dec.feed(b, i); err != nil {
				return "", false, err
			}
			out = append(out, b)
			i++
			continue
		}
		if i+2 >= len(s) {
			return "", false, &EscapeError{Kind: KindShort, Offset: i, wrap: ErrShortEscape}
		}
		hi := hexTable[s[i+1]]
		lo := hexTable[s[i+2]]
		if hi == 0xFF {
			return "", false, &EscapeError{Kind: KindBadHex, Offset: i + 1, wrap: ErrBadHex}
		}
		if lo == 0xFF {
			return "", false, &EscapeError{Kind: KindBadHex, Offset: i + 2, wrap: ErrBadHex}
		}
		v := hi<<4 | lo
		if err := dec.feed(v, i); err != nil {
			return "", false, err
		}
		if isUnreserved(v) {
			out = append(out, v)
		} else {
			const hex = "0123456789ABCDEF"
			out = append(out, '%', hex[v>>4], hex[v&0x0F])
		}
		i += 3
	}
	if err := dec.flush(); err != nil {
		return "", false, err
	}
	outStr := string(out)
	return outStr, outStr != s, nil
}

// isUnreserved reports whether b is an RFC 3986 unreserved character
// (ALPHA / DIGIT / "-" / "." / "_" / "~"). Only escapes of these characters
// may be decoded, because doing so can never change how the component is
// parsed into structure.
func isUnreserved(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z',
		b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '-', '.', '_', '~':
		return true
	}
	return false
}

// decoder incrementally validates the logical byte stream as UTF-8 without
// ever materializing a second decoded copy of the whole input.
type decoder struct {
	pending []byte
	start   int
	want    int
}

func newDecoder() decoder { return decoder{pending: make([]byte, 0, 4)} }

func (d *decoder) feed(b byte, off int) *EscapeError {
	if b < 0x80 {
		return d.flush()
	}
	if len(d.pending) == 0 {
		// C0/C1 are overlong; F5..FF can never start a valid sequence.
		if b < 0xC2 || b > 0xF4 {
			return &EscapeError{Kind: KindUTF8, Offset: off, wrap: ErrInvalidUTF8}
		}
		d.pending = d.pending[:0]
		d.start = off
		d.want = 2
		switch {
		case b >= 0xF0:
			d.want = 4
		case b >= 0xE0:
			d.want = 3
		}
		d.pending = append(d.pending, b)
		return nil
	}
	if b < 0x80 || b > 0xBF {
		return &EscapeError{Kind: KindUTF8, Offset: d.start, wrap: ErrInvalidUTF8}
	}
	d.pending = append(d.pending, b)
	if len(d.pending) == d.want {
		if r, _ := utf8.DecodeRune(d.pending); r == utf8.RuneError {
			return &EscapeError{Kind: KindUTF8, Offset: d.start, wrap: ErrInvalidUTF8}
		}
		d.pending = d.pending[:0]
	}
	return nil
}

func (d *decoder) flush() *EscapeError {
	if len(d.pending) > 0 {
		return &EscapeError{Kind: KindUTF8, Offset: d.start, wrap: ErrInvalidUTF8}
	}
	return nil
}
