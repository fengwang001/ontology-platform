// Package u8 decodes and encodes UTF-8 one byte at a time without using
// unicode/utf8. Every Token starts at an absolute input offset.
package u8

import "ontology/scalar"

// Kind enumerates token outcomes.
const (
	OK   = iota // one valid scalar
	Bad         // one invalid unit (caller emits U+FFFD)
	Need        // incomplete prefix, waiting for more bytes
)

// Token is one decoded unit. Len is the bytes consumed; when Kind==Need only
// a prefix is buffered and Len is 0 (nothing is finalized).
type Token struct {
	R      rune
	Kind   int
	Offset int
	Len    int
}

// Decoder is a streaming UTF-8 decoder. Prefix holds at most 3 bytes.
type Decoder struct {
	prefix [3]byte
	need   int  // continuation bytes still required
	have   int  // continuation bytes already buffered
	start  int  // absolute offset of the in-progress lead byte
	first  bool // second byte range check still pending
	val    rune // accumulated scalar
}

func cont(b byte) bool { return b >= 0x80 && b <= 0xBF }

// Step feeds one byte at absolute offset off. On a finalized token it is
// returned with Len>0; a Need token means nothing is consumed yet.
func (d *Decoder) Step(b byte, off int) Token {
	if d.need > 0 {
		if d.first || !cont(b) {
			if d.first && cont(b) && secondOK(d.prefix[0], b) {
				d.first = false
				d.prefix[d.have] = b
				d.have++
				d.need--
				d.val = d.val<<6 | rune(b&0x3F)
				return Token{Kind: Need}
			}
			t := Token{Kind: Bad, Offset: d.start, Len: 1 + d.have}
			d.need = 0
			return t // b is returned to the caller for re-parse
		}
		d.prefix[d.have] = b
		d.have++
		d.need--
		d.val = d.val<<6 | rune(b&0x3F)
		if d.need == 0 {
			t := Token{R: d.val, Kind: OK, Offset: d.start, Len: 1 + d.have}
			return t
		}
		return Token{Kind: Need}
	}
	switch {
	case b < 0x80:
		return Token{R: rune(b), Kind: OK, Offset: off, Len: 1}
	case b < 0xC2: // 80..BF stray, C0/C1 always invalid
		return Token{Kind: Bad, Offset: off, Len: 1}
	case b <= 0xDF:
		d.prefix[0], d.start, d.need, d.have = b, off, 1, 1
	case b <= 0xEF:
		d.prefix[0], d.start, d.need, d.have = b, off, 2, 1
	case b <= 0xF4:
		d.prefix[0], d.start, d.need, d.have = b, off, 3, 1
	default: // F5..FF
		return Token{Kind: Bad, Offset: off, Len: 1}
	}
	d.first, d.val = true, rune(b&0x0F)
	return Token{Kind: Need}
}

func secondOK(lead, b byte) bool {
	switch lead {
	case 0xE0:
		return b >= 0xA0
	case 0xED:
		return b <= 0x9F
	case 0xF0:
		return b >= 0x90
	case 0xF4:
		return b <= 0x8F
	}
	return true // all other multi-byte leads accept 80..BF
}

// PrefixLen reports how many bytes of an unfinished prefix are buffered.
func (d *Decoder) PrefixLen() int {
	if d.need == 0 {
		return 0
	}
	return d.have
}

// PrefixStart reports the offset of the buffered lead byte.
func (d *Decoder) PrefixStart() int { return d.start }

// EOF finalizes the stream; a residual prefix is one truncated bad unit.
func (d *Decoder) EOF() Token {
	if d.need == 0 {
		return Token{Kind: Need}
	}
	t := Token{Kind: Bad, Offset: d.start, Len: d.have}
	d.need = 0
	return t
}

// Encode appends the UTF-8 encoding of r to dst.
func Encode(dst []byte, r rune) []byte {
	switch {
	case r < 0x80:
		return append(dst, byte(r))
	case r < 0x800:
		return append(dst, 0xC0|byte(r>>6), 0x80|byte(r&0x3F))
	case r < 0x10000:
		return append(dst, 0xE0|byte(r>>12), 0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	default:
		return append(dst, 0xF0|byte(r>>18), 0x80|byte((r>>12)&0x3F),
			0x80|byte((r>>6)&0x3F), 0x80|byte(r&0x3F))
	}
}

// ValidScalar re-exports scalar validity for encoders in upper layers.
func ValidScalar(r rune) bool { return scalar.Valid(r) }
