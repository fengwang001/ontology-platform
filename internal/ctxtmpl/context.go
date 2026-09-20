package ctxtmpl

// parseState is the state of the HTML mini-parser.
type parseState int

const (
	stText parseState = iota
	stComment
	stTagName
	stTagGap // inside a tag, between attributes
	stAttrName
	stAttrNameGap // attribute name seen, whitespace before possible '='
	stAfterEq
	stValDouble
	stValSingle
	stValUnquoted
)

// interpolationContext describes how a value at one interpolation point
// must be escaped and validated.
type interpolationContext struct {
	kind       contextKind
	urlValue   bool
	atURLStart bool
}

// scanner walks template text and tracks the HTML context.
type scanner struct {
	state    parseState
	attrName []byte
	urlAttr  bool
	valStart bool // non-whitespace content seen in current value
	dashRun  int  // consecutive '-' inside a comment
	// pendingOpen counts a trailing "<" or "</" whose first name byte was
	// not seen yet; an interpolation there sits in tag-name position.
	pendingOpen int
}

func newScanner() *scanner {
	return &scanner{state: stText}
}

// consumeLiteral advances the parser over template text that contains no
// interpolation delimiters.
func (sc *scanner) consumeLiteral(seg string) error {
	sc.pendingOpen = 0
	for i := 0; i < len(seg); i++ {
		switch sc.state {
		case stText:
			i = sc.consumeText(seg, i)
		case stComment:
			i = sc.consumeComment(seg, i)
		default:
			i = sc.consumeTag(seg, i)
		}
	}
	return nil
}

func (sc *scanner) consumeText(seg string, i int) int {
	if seg[i] == '<' {
		if hasPrefixAt(seg, i, "<!--") {
			sc.state = stComment
			sc.dashRun = 0
			return i + 3
		}
		if j, ok := tagOpenEnd(seg, i); ok {
			sc.state = stTagName
			return j
		}
		if i+1 == len(seg) || i+1 == len(seg)-1 && seg[i+1] == '/' {
			sc.pendingOpen = len(seg) - i
		}
	}
	return i
}

// openInterpolation reports an error if the parser is positioned where a
// tag name or attribute name is required (instead of a value position).
func (sc *scanner) openInterpolation() error {
	if sc.pendingOpen > 0 {
		sc.pendingOpen = 0
		return ErrInterpolationInTagName
	}
	return nil
}

func (sc *scanner) consumeComment(seg string, i int) int {
	switch seg[i] {
	case '-':
		sc.dashRun++
	default:
		sc.dashRun = 0
	}
	if seg[i] == '>' && sc.dashRun >= 2 {
		sc.state = stText
		sc.dashRun = 0
	}
	return i
}

func hasPrefixAt(s string, i int, prefix string) bool {
	return i+len(prefix) <= len(s) && s[i:i+len(prefix)] == prefix
}

// tagOpenEnd returns the index after "<" or "</" when a tag starts at i.
func tagOpenEnd(seg string, i int) (int, bool) {
	if i+1 < len(seg) && isAlpha(seg[i+1]) {
		return i + 1, true
	}
	if i+2 < len(seg) && seg[i+1] == '/' && isAlpha(seg[i+2]) {
		return i + 2, true
	}
	return i, false
}

func isAlpha(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// finish verifies that every tag and quoted value was closed.
func (sc *scanner) finish() error {
	switch sc.state {
	case stValDouble, stValSingle:
		return ErrUnclosedQuote
	case stTagName, stTagGap, stAttrName, stAttrNameGap, stAfterEq, stValUnquoted:
		return ErrUnclosedTag
	default:
		return nil
	}
}
