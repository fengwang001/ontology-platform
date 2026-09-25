package u16

import "ontology/scalar"

type Token struct {
	R       rune
	Start   int
	End     int
	Valid   bool
}

type Decoder struct {
	big      bool
	odd      byte
	hasOdd   bool
	high     rune
	highAt   int
	waitHigh bool
	pos      int
}

func NewDecoder(big bool, start int) *Decoder { return &Decoder{big: big, pos: start} }

func (d *Decoder) Feed(p []byte) []Token {
	tokens := []Token{}
	i := 0
	if d.hasOdd && len(p) > 0 {
		tokens = d.pair(tokens, d.odd, p[0], d.pos-1)
		d.hasOdd = false
		i = 1
	}
	for ; i+1 < len(p); i += 2 {
		tokens = d.pair(tokens, p[i], p[i+1], d.pos+i)
	}
	if i < len(p) {
		d.odd, d.hasOdd = p[i], true
	}
	d.pos += len(p)
	return tokens
}

func (d *Decoder) End() Token {
	switch {
	case d.hasOdd:
		start := d.pos - 1
		d.hasOdd = false
		return Token{R: scalar.Replacement, Start: start, End: d.pos, Valid: false}
	case d.waitHigh:
		start := d.highAt
		d.waitHigh = false
		return Token{R: scalar.Replacement, Start: start, End: start + 2, Valid: false}
	default:
		return Token{}
	}
}

func (d *Decoder) Pending() int {
	if d.hasOdd {
		return 1
	}
	if d.waitHigh {
		return 2
	}
	return 0
}

func (d *Decoder) pair(tokens []Token, lowByte, highByte byte, start int) []Token {
	value := d.unit(lowByte, highByte)
	if d.waitHigh {
		oldStart := d.highAt
		d.waitHigh = false
		if scalar.IsLowSurrogate(value) {
			return append(tokens, Token{R: scalar.FromSurrogates(d.high, value), Start: oldStart, End: start + 2, Valid: true})
		}
		tokens = append(tokens, Token{R: scalar.Replacement, Start: oldStart, End: oldStart + 2, Valid: false})
	}
	switch {
	case scalar.IsHighSurrogate(value):
		d.high, d.highAt, d.waitHigh = value, start, true
	case scalar.IsLowSurrogate(value):
		tokens = append(tokens, Token{R: scalar.Replacement, Start: start, End: start + 2, Valid: false})
	default:
		tokens = append(tokens, Token{R: value, Start: start, End: start + 2, Valid: true})
	}
	return tokens
}

func (d *Decoder) unit(first, second byte) rune {
	if d.big {
		return rune(first)<<8 | rune(second)
	}
	return rune(second)<<8 | rune(first)
}

func AppendLE(out []byte, r rune) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		high, low := 0xD800+r>>10, 0xDC00+r&0x3FF
		return append(out, byte(high), byte(high>>8), byte(low), byte(low>>8))
	}
	return append(out, byte(r), byte(r>>8))
}
func AppendBE(out []byte, r rune) []byte {
	if r >= 0x10000 {
		r -= 0x10000
		high, low := 0xD800+r>>10, 0xDC00+r&0x3FF
		return append(out, byte(high>>8), byte(high), byte(low>>8), byte(low))
	}
	return append(out, byte(r>>8), byte(r))
}
