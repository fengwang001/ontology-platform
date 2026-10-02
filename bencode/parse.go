package bencode

import (
	"errors"
	"math"
	"strings"
)

// errIncomplete is an internal sentinel meaning the buffer does not yet
// hold enough bytes to decide. It never escapes the package.
var errIncomplete = errors.New("bencode: incomplete input")

// parseError carries a rejection reason plus the offset of the offending
// byte relative to the start of the parser's buffer.
type parseError struct {
	reason error
	off    int
}

func (e *parseError) Error() string { return e.reason.Error() }

// parser is a single-pass, non-resumable parser over a byte slice. The
// Decoder re-runs it over the pending buffer on every Feed; because it is
// a pure function of the buffered bytes, the delivered values, consumed
// counts and error offsets are independent of how the input was chunked.
type parser struct {
	buf       []byte
	pos       int
	maxString uint64
	maxDepth  int
}

func (p *parser) fail(reason error, off int) error {
	return &parseError{reason: reason, off: off}
}

func (p *parser) peek() (byte, bool) {
	if p.pos >= len(p.buf) {
		return 0, false
	}
	return p.buf[p.pos], true
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// value parses one value. depth is the nesting level the value sits at,
// with top-level values at depth 1; only containers are depth-checked.
func (p *parser) value(depth int) (*Value, error) {
	b, ok := p.peek()
	if !ok {
		return nil, errIncomplete
	}
	switch {
	case b == 'i':
		return p.integer()
	case b == 'l':
		if depth > p.maxDepth {
			return nil, p.fail(ErrDepthExceeded, p.pos)
		}
		p.pos++
		return p.list(depth)
	case b == 'd':
		if depth > p.maxDepth {
			return nil, p.fail(ErrDepthExceeded, p.pos)
		}
		p.pos++
		return p.dict(depth)
	case isDigit(b):
		s, err := p.string()
		if err != nil {
			return nil, err
		}
		return &Value{Kind: KindString, Str: s}, nil
	default:
		return nil, p.fail(ErrBadLeadingByte, p.pos)
	}
}

func (p *parser) integer() (*Value, error) {
	p.pos++ // consume 'i'
	neg := false
	if b, ok := p.peek(); ok && b == '-' {
		neg = true
		p.pos++
	}
	limit := uint64(math.MaxInt64)
	if neg {
		limit = 1 << 63
	}
	var mag uint64
	digits := 0
	for {
		b, ok := p.peek()
		if !ok {
			return nil, errIncomplete
		}
		if b == 'e' {
			if digits == 0 {
				// Covers "ie" and "i-e": 'e' where a digit is required.
				return nil, p.fail(ErrSyntax, p.pos)
			}
			p.pos++
			v := &Value{Kind: KindInt}
			switch {
			case !neg:
				v.Int = int64(mag)
			case mag == 1<<63:
				v.Int = math.MinInt64
			default:
				v.Int = -int64(mag)
			}
			return v, nil
		}
		if !isDigit(b) {
			return nil, p.fail(ErrSyntax, p.pos)
		}
		d := uint64(b - '0')
		if digits == 0 && d == 0 {
			// Leading zero: the verdict depends on the next byte, so
			// wait for it if it has not arrived yet.
			if p.pos+1 >= len(p.buf) {
				return nil, errIncomplete
			}
			switch nb := p.buf[p.pos+1]; {
			case isDigit(nb):
				return nil, p.fail(ErrIntLeadingZero, p.pos+1)
			case nb == 'e' && neg:
				return nil, p.fail(ErrNegativeZero, p.pos)
			}
		}
		if mag > (limit-d)/10 {
			return nil, p.fail(ErrIntOverflow, p.pos)
		}
		mag = mag*10 + d
		digits++
		p.pos++
	}
}

// string parses "<len>:<bytes>". The caller has already established that
// the current byte is a digit.
func (p *parser) string() (string, error) {
	var length uint64
	digits := 0
	for {
		b, ok := p.peek()
		if !ok {
			return "", errIncomplete
		}
		if b == ':' {
			p.pos++
			break
		}
		if !isDigit(b) {
			return "", p.fail(ErrSyntax, p.pos)
		}
		d := uint64(b - '0')
		if digits == 0 && d == 0 {
			if p.pos+1 >= len(p.buf) {
				return "", errIncomplete
			}
			if nb := p.buf[p.pos+1]; isDigit(nb) {
				return "", p.fail(ErrLenLeadingZero, p.pos+1)
			}
		}
		if d > p.maxString || length > (p.maxString-d)/10 {
			return "", p.fail(ErrStringTooLong, p.pos)
		}
		length = length*10 + d
		digits++
		p.pos++
	}
	if uint64(len(p.buf)-p.pos) < length {
		return "", errIncomplete
	}
	s := string(p.buf[p.pos : p.pos+int(length)])
	p.pos += int(length)
	return s, nil
}

// list parses the body of a list; the opening 'l' is already consumed.
func (p *parser) list(depth int) (*Value, error) {
	v := &Value{Kind: KindList}
	for {
		b, ok := p.peek()
		if !ok {
			return nil, errIncomplete
		}
		if b == 'e' {
			p.pos++
			return v, nil
		}
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.List = append(v.List, item)
	}
}

// dict parses the body of a dictionary; the opening 'd' is already
// consumed. Keys must be byte strings in strictly ascending byte order
// (shorter prefixes first, the empty string smallest).
func (p *parser) dict(depth int) (*Value, error) {
	v := &Value{Kind: KindDict}
	prev := ""
	hasPrev := false
	for {
		b, ok := p.peek()
		if !ok {
			return nil, errIncomplete
		}
		if b == 'e' {
			p.pos++
			return v, nil
		}
		if !isDigit(b) {
			return nil, p.fail(ErrKeyNotString, p.pos)
		}
		keyOff := p.pos
		key, err := p.string()
		if err != nil {
			return nil, err
		}
		if hasPrev {
			switch strings.Compare(prev, key) {
			case 0:
				return nil, p.fail(ErrDuplicateKey, keyOff)
			case 1:
				return nil, p.fail(ErrKeyOutOfOrder, keyOff)
			}
		}
		val, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		v.Dict = append(v.Dict, DictEntry{Key: key, Val: val})
		prev, hasPrev = key, true
	}
}
