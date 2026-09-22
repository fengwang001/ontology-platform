package rangespec

import "strings"

type parser struct {
	s   string
	pos int
}

// Parse parses a Range header value such as "bytes=0-99,200-,-500".
//
// The grammar accepted is, deliberately, strictly:
//
//	"bytes" OWS "=" OWS ( spec ( OWS "," OWS spec )* )?
//	spec = int "-" (int)? | "-" int
//
// where OWS is optional spaces/tabs and int is one or more ASCII digits.
// A header without any range spec ("bytes=" or "bytes=,") is a syntax error.
func Parse(header string) ([]RangeSpec, error) {
	p := &parser{s: header}
	p.skipOWS()
	if !p.consumeWordFold("bytes") {
		return nil, &SyntaxError{Offset: p.pos, Reason: `expected range unit "bytes"`}
	}
	p.skipOWS()
	if !p.consumeByte('=') {
		return nil, &SyntaxError{Offset: p.pos, Reason: `expected "=" after range unit`}
	}
	p.skipOWS()

	specs := []RangeSpec{}
	for {
		spec, err := p.parseSpec()
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		p.skipOWS()
		if p.pos >= len(p.s) {
			break
		}
		if !p.peekByte(',') {
			return nil, &SyntaxError{Offset: p.pos, Reason: `expected "," between range specs`}
		}
		p.pos++
		p.skipOWS()
		if p.pos >= len(p.s) {
			return nil, &SyntaxError{Offset: p.pos - 1, Reason: "trailing comma, expected range spec"}
		}
	}
	return specs, nil
}

func (p *parser) parseSpec() (RangeSpec, error) {
	switch {
	case p.peekDigit():
		first, err := p.parseNumber()
		if err != nil {
			return RangeSpec{}, err
		}
		if !p.consumeByte('-') {
			return RangeSpec{}, &SyntaxError{Offset: p.pos, Reason: `expected "-" after first byte position`}
		}
		if !p.peekDigit() {
			return RangeSpec{Kind: KindOpenEnd, Start: first}, nil
		}
		last, err := p.parseNumber()
		if err != nil {
			return RangeSpec{}, err
		}
		return RangeSpec{Kind: KindClosed, Start: first, End: last}, nil
	case p.peekByte('-'):
		p.pos++
		if !p.peekDigit() {
			return RangeSpec{}, &SyntaxError{Offset: p.pos, Reason: "expected digit after \"-\" in suffix range"}
		}
		n, err := p.parseNumber()
		if err != nil {
			return RangeSpec{}, err
		}
		return RangeSpec{Kind: KindSuffix, Suffix: n}, nil
	default:
		return RangeSpec{}, &SyntaxError{Offset: p.pos, Reason: "expected range spec (digit or \"-\")"}
	}
}

func (p *parser) parseNumber() (int64, error) {
	start := p.pos
	var n int64
	for p.pos < len(p.s) && isDigit(p.s[p.pos]) {
		d := int64(p.s[p.pos] - '0')
		if n > (1<<63-1-d)/10 {
			return 0, &SyntaxError{Offset: start, Reason: "number overflows int64"}
		}
		n = n*10 + d
		p.pos++
	}
	return n, nil
}

func (p *parser) skipOWS() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func (p *parser) peekByte(b byte) bool {
	return p.pos < len(p.s) && p.s[p.pos] == b
}

func (p *parser) peekDigit() bool {
	return p.pos < len(p.s) && isDigit(p.s[p.pos])
}

func (p *parser) consumeByte(b byte) bool {
	if p.peekByte(b) {
		p.pos++
		return true
	}
	return false
}

func (p *parser) consumeWordFold(word string) bool {
	if p.pos+len(word) > len(p.s) {
		return false
	}
	if !strings.EqualFold(p.s[p.pos:p.pos+len(word)], word) {
		return false
	}
	p.pos += len(word)
	return true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
