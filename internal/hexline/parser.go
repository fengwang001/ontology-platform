package hexline

// Parser incrementally parses one chunk-size line:
//
//	1*HEXDIG *( OWS ";" OWS chunk-ext ) CRLF
//
// Extensions are skipped, not returned. Input may be split at any byte
// boundary; Feed consumes only the bytes it needs.
type Parser struct {
	maxLine int

	off   int // total bytes consumed across all Feed calls
	state state

	size uint64 // accumulated chunk size
	seen bool   // at least one hex digit seen

	quoted  bool // inside a double-quoted extension value
	escaped bool // previous byte was a backslash inside quotes
}

type state int

const (
	stDigits    state = iota
	stBeforeExt       // after digits or value, waiting for ';' or CR
	stExtStart        // just consumed ';'
	stName
	stAfterName // after name, waiting for '=' or ';'
	stValue     // unquoted value
	stQuote
	stCR
	stDone
)

// NewParser returns a parser whose size line (including the trailing CRLF)
// must not exceed maxLine bytes. maxLine <= 0 means unlimited.
func NewParser(maxLine int) *Parser {
	return &Parser{maxLine: maxLine, state: stDigits}
}

// Reset restores the parser to its initial state for reuse.
func (p *Parser) Reset() {
	*p = Parser{maxLine: p.maxLine, state: stDigits}
}

// Feed consumes bytes from in. It returns the number of bytes consumed and,
// when the full size line (including CRLF) is present, the parsed chunk size.
// A non-nil error is terminal for this Parser.
func (p *Parser) Feed(in []byte) (consumed int, size uint64, err error) {
	for consumed < len(in) {
		if p.maxLine > 0 && p.off >= p.maxLine {
			return consumed, 0, &Error{Kind: KindLineTooLong, Offset: p.off}
		}
		c := in[consumed]

		switch p.state {
		case stDigits:
			d, ok := hexDigit(c)
			if !ok {
				if p.seen && (c == ';' || c == '\r') {
					p.state = stBeforeExt
					continue
				}
				return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
			}
			p.size = p.size<<4 | uint64(d)
			p.seen = true
		case stBeforeExt:
			switch c {
			case ';':
				p.state = stExtStart
			case '\r':
				p.state = stCR
			default:
				return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
			}
		case stExtStart:
			if isTokenChar(c) {
				p.state = stName
			} else {
				return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
			}
		case stName:
			if isTokenChar(c) {
				// extension name characters are skipped
			} else if c == '=' {
				p.state = stAfterName
			} else if c == ';' {
				p.state = stExtStart
			} else if c == '\r' {
				p.state = stCR
			} else {
				return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
			}
		case stAfterName:
			switch c {
			case '"':
				p.quoted = true
				p.escaped = false
				p.state = stQuote
			case ';':
				p.state = stExtStart
			case '\r':
				p.state = stCR
			default:
				p.state = stValue
			}
		case stValue:
			switch c {
			case ';':
				p.state = stExtStart
			case '\r':
				p.state = stCR
			default:
				if !isTokenChar(c) {
					return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
				}
			}
		case stQuote:
			switch {
			case p.escaped:
				p.escaped = false
			case c == '\\':
				p.escaped = true
			case c == '"':
				p.quoted = false
				p.state = stBeforeExt
			}
		case stCR:
			if c != '\n' {
				return consumed, 0, &Error{Kind: KindNonHex, Offset: p.off}
			}
			p.advance(&consumed)
			p.state = stDone
			return consumed, p.size, nil
		case stDone:
			return consumed, p.size, nil
		}

		p.advance(&consumed)
	}
	return consumed, 0, nil
}

func (p *Parser) advance(consumed *int) {
	*consumed++
	p.off++
}

// InQuote reports whether the parser is currently inside a quoted extension
// value.
func (p *Parser) InQuote() bool { return p.quoted }

// Close signals that no more bytes will arrive. It returns an error if the
// size line was never completed; an unterminated quoted extension value is
// reported as KindUnterminatedQuote rather than a generic truncation.
func (p *Parser) Close() error {
	if p.state == stDone {
		return nil
	}
	if p.quoted {
		return &Error{Kind: KindUnterminatedQuote, Offset: p.off}
	}
	return &Error{Kind: KindNonHex, Offset: p.off}
}

// Done reports whether the full size line including CRLF was consumed.
func (p *Parser) Done() bool { return p.state == stDone }

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10, true
	}
	return 0, false
}

func isTokenChar(c byte) bool {
	switch c {
	case '"', ' ', '\t', '(', ')', ',', '/', ':', ';', '<', '=', '>',
		'?', '@', '[', '\\', ']', '{', '}', 0x7f:
		return false
	}
	return c > 0x20
}
