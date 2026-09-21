package ctxtmpl

import "strings"

// feedLiteral advances the parser over raw template text (the portions between
// interpolations). hasNext reports that seg is immediately followed by an
// interpolation, which lets the parser classify a trailing '<' as a tag start.
func (s *scanner) feedLiteral(seg string, hasNext bool) {
	for i := 0; i < len(seg); {
		ch := seg[i]
		switch s.st {
		case stText:
			i = s.feedText(seg, i, hasNext)
		case stComment:
			if strings.HasPrefix(seg[i:], "-->") {
				s.st = stText
				i += 3
				continue
			}
			i++
		case stTagName:
			switch {
			case ch == '>':
				s.closeTag()
			case isASCIISpace(ch) || ch == '/':
				s.st = stTagGap
			}
			i++
		case stTagGap:
			switch {
			case ch == '>':
				s.closeTag()
			case isASCIISpace(ch) || ch == '/':
			case ch == '=':
				// Browsers allow whitespace between an attribute name and '='.
				s.curURL = isURLAttr(s.nameBuf)
				s.urlPrefix = s.urlPrefix[:0]
				s.st = stAttrEq
			case isASCIIAlpha(ch):
				s.st = stAttrName
				s.nameBuf = append(s.nameBuf[:0], lowerASCII(ch))
			}
			i++
		case stAttrName:
			i = s.feedAttrName(seg, i)
		case stAttrEq:
			i = s.feedAttrEq(seg, i)
		case stAttrDouble:
			i = s.feedQuoted(seg, i, '"', stAttrDouble)
		case stAttrSingle:
			i = s.feedQuoted(seg, i, '\'', stAttrSingle)
		case stAttrUnquoted:
			i = s.feedUnquoted(seg, i)
		}
	}
}

// feedText handles one byte in document text and returns the next index.
func (s *scanner) feedText(seg string, i int, hasNext bool) int {
	if strings.HasPrefix(seg[i:], "<!--") {
		s.st = stComment
		return i + 4
	}
	ch := seg[i]
	if ch == '<' {
		switch {
		case i+1 < len(seg) && isASCIIAlpha(seg[i+1]):
			s.st = stTagName
			return i + 1
		case i+2 < len(seg) && seg[i+1] == '/' && isASCIIAlpha(seg[i+2]):
			s.st = stTagName
			return i + 2
		case hasNext && i+1 == len(seg):
			// '<' (possibly '</') directly precedes an interpolation: the
			// interpolation sits where a tag name is required.
			s.st = stTagName
			return i + 1
		case hasNext && i+2 == len(seg) && seg[i+1] == '/':
			s.st = stTagName
			return i + 2
		}
	}
	return i + 1
}

// feedAttrName handles bytes while reading an attribute name.
func (s *scanner) feedAttrName(seg string, i int) int {
	ch := seg[i]
	switch {
	case ch == '=':
		s.curURL = isURLAttr(s.nameBuf)
		s.urlPrefix = s.urlPrefix[:0]
		s.st = stAttrEq
	case ch == '>':
		s.closeTag()
	case isASCIISpace(ch) || ch == '/':
		s.st = stTagGap
	default:
		s.nameBuf = append(s.nameBuf, lowerASCII(ch))
	}
	return i + 1
}

// feedAttrEq handles the gap between '=' and the attribute value.
func (s *scanner) feedAttrEq(seg string, i int) int {
	ch := seg[i]
	switch {
	case isASCIISpace(ch):
	case ch == '"':
		s.st = stAttrDouble
		s.beginQuotedValue()
	case ch == '\'':
		s.st = stAttrSingle
		s.beginQuotedValue()
	case ch == '>':
		s.closeTag()
	default:
		s.st = stAttrUnquoted
		s.urlPrefix = s.urlPrefix[:0]
		s.appendURLPrefixByte(ch)
	}
	return i + 1
}

// feedQuoted handles bytes inside a quoted attribute value.
func (s *scanner) feedQuoted(seg string, i int, quote byte, st topState) int {
	ch := seg[i]
	if ch == quote {
		s.st = stTagGap
		return i + 1
	}
	s.appendURLPrefixByte(ch)
	return i + 1
}

// feedUnquoted handles bytes inside an unquoted attribute value.
func (s *scanner) feedUnquoted(seg string, i int) int {
	ch := seg[i]
	switch {
	case ch == '>':
		s.closeTag()
	case isASCIISpace(ch):
		s.st = stTagGap
	default:
		s.appendURLPrefixByte(ch)
	}
	return i + 1
}

// beginQuotedValue resets value tracking when a quoted value opens.
func (s *scanner) beginQuotedValue() {
	s.urlPrefix = s.urlPrefix[:0]
}

// feedInterpolation marks that a value was emitted at the current position
// and folds the value into the URL prefix accumulator, so a later scheme
// check sees the concatenation of everything emitted into the attribute.
func (s *scanner) feedInterpolation(value string) {
	if s.st == stAttrEq {
		// An interpolation directly after '=' begins an unquoted value.
		s.st = stAttrUnquoted
		s.urlPrefix = s.urlPrefix[:0]
	}
	switch s.st {
	case stAttrDouble, stAttrSingle, stAttrUnquoted:
		s.appendURLPrefixString(value)
	}
}

// appendURLPrefixByte folds one literal byte of an attribute value into the
// URL scheme probe prefix. Only URL attributes are tracked, whitespace is
// dropped (matching checkDangerousURL), and the prefix is capped because a
// scheme never extends beyond maxSchemeProbe bytes.
func (s *scanner) appendURLPrefixByte(ch byte) {
	if !s.curURL || isASCIISpace(ch) || len(s.urlPrefix) >= maxSchemeProbe {
		return
	}
	s.urlPrefix = append(s.urlPrefix, lowerASCII(ch))
}

// appendURLPrefixString folds an interpolated value into the probe prefix.
func (s *scanner) appendURLPrefixString(value string) {
	for i := 0; i < len(value); i++ {
		s.appendURLPrefixByte(value[i])
	}
}

// urlValuePrefix returns the scheme probe prefix accumulated so far for the
// attribute value currently being rendered.
func (s *scanner) urlValuePrefix() string {
	return string(s.urlPrefix)
}
