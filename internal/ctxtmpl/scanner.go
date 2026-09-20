package ctxtmpl

import "strings"

// state identifies where the scanner currently sits inside the template.
type state int

const (
	stText          state = iota // HTML text
	stTag                        // inside <...>, outside any attribute value
	stAttrDouble                 // inside a "..." attribute value
	stAttrSingle                 // inside a '...' attribute value
	stAttrUnquoted               // inside a bare attribute value
	stComment                    // inside <!-- ... -->
)

// scanner tracks the HTML context while Render walks the template.
type scanner struct {
	state      state
	attr       []byte // current attribute name, lowercased
	pending    string // attribute name seen just before '='
	valueAttr  string // attribute owning the value we are inside
	valueStart bool   // value so far is empty or whitespace only
	afterEq    bool   // last significant char in the tag was '='
}

// step consumes one literal unit of template at offset i (a single
// byte, or a multi-byte <!-- / --> marker) and returns the next offset.
func (sc *scanner) step(out *strings.Builder, tmpl string, i int) int {
	c := tmpl[i]
	switch sc.state {
	case stText:
		if c == '<' && i+1 < len(tmpl) {
			if i+4 <= len(tmpl) && tmpl[i:i+4] == "<!--" {
				out.WriteString("<!--")
				sc.state = stComment
				return i + 4
			}
			if n := tmpl[i+1]; isLetter(n) || n == '/' || n == '!' {
				out.WriteByte(c)
				sc.state = stTag
				sc.attr = sc.attr[:0]
				sc.pending, sc.afterEq = "", false
				return i + 1
			}
		}
		out.WriteByte(c)
		return i + 1
	case stTag:
		return sc.stepTag(out, c, i)
	case stAttrDouble:
		if c == '"' {
			out.WriteByte(c)
			sc.leaveValue()
			return i + 1
		}
		sc.consumeValueByte(c)
		out.WriteByte(c)
		return i + 1
	case stAttrSingle:
		if c == '\'' {
			out.WriteByte(c)
			sc.leaveValue()
			return i + 1
		}
		sc.consumeValueByte(c)
		out.WriteByte(c)
		return i + 1
	case stAttrUnquoted:
		if isSpace(c) {
			out.WriteByte(c)
			sc.leaveValue()
			return i + 1
		}
		if c == '>' {
			out.WriteByte(c)
			sc.leaveValue()
			sc.state = stText
			return i + 1
		}
		sc.consumeValueByte(c)
		out.WriteByte(c)
		return i + 1
	case stComment:
		if c == '-' && i+3 <= len(tmpl) && tmpl[i:i+3] == "-->" {
			out.WriteString("-->")
			sc.state = stText
			return i + 3
		}
		out.WriteByte(c)
		return i + 1
	}
	out.WriteByte(c)
	return i + 1
}

// stepTag handles one byte while inside a tag but outside any value.
func (sc *scanner) stepTag(out *strings.Builder, c byte, i int) int {
	out.WriteByte(c)
	switch {
	case c == '>':
		sc.state = stText
		sc.attr = sc.attr[:0]
		sc.pending, sc.afterEq = "", false
	case c == '"' || c == '\'':
		sc.beginValue(c)
	case c == '=':
		sc.pending = string(sc.attr)
		sc.afterEq = true
	case isSpace(c):
		if !sc.afterEq {
			sc.attr = sc.attr[:0]
		}
	case isAttrChar(c):
		if sc.afterEq {
			// First byte of an unquoted attribute value.
			sc.valueAttr = sc.pending
			sc.valueStart = true
			sc.afterEq = false
			sc.state = stAttrUnquoted
			sc.consumeValueByte(c)
		} else {
			sc.attr = append(sc.attr, lower(c))
		}
	}
	return i + 1
}

// beginValue enters a quoted attribute value started by quote.
func (sc *scanner) beginValue(quote byte) {
	sc.valueAttr = ""
	if sc.afterEq {
		sc.valueAttr = sc.pending
	}
	sc.valueStart = true
	sc.afterEq = false
	sc.attr = sc.attr[:0]
	sc.pending = ""
	if quote == '"' {
		sc.state = stAttrDouble
	} else {
		sc.state = stAttrSingle
	}
}

// leaveValue returns to plain tag state after a value ends.
func (sc *scanner) leaveValue() {
	sc.state = stTag
	sc.valueAttr = ""
	sc.attr = sc.attr[:0]
	sc.pending, sc.afterEq = "", false
}

func (sc *scanner) consumeValueByte(c byte) {
	sc.valueStart = sc.valueStart && isSpace(c)
}

// finish validates the final state at end of template.
func (sc *scanner) finish() error {
	switch sc.state {
	case stAttrDouble, stAttrSingle:
		return ErrUnclosedQuote
	case stTag, stAttrUnquoted:
		return ErrUnclosedTag
	case stComment:
		return ErrUnclosedComment
	}
	return nil
}

func isLetter(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

func isAttrChar(c byte) bool {
	return isLetter(c) || c >= '0' && c <= '9' || c == '-' || c == '_'
}
