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
				s.urlEndValue()
			case ch == '=':
				// Browsers allow whitespace between an attribute name and '='.
				s.curURL = isURLAttr(s.nameBuf)
				s.valueSeen = false
				s.valueSpace = true
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
		s.valueSeen = false
		s.valueSpace = true
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
		s.valueSeen = true
		s.valueSpace = false
		s.urlStartValue()
		s.urlEatByte(ch)
	}
	return i + 1
}

// feedQuoted handles bytes inside a quoted attribute value.
func (s *scanner) feedQuoted(seg string, i int, quote byte, st topState) int {
	ch := seg[i]
	if ch == quote {
		s.st = stTagGap
		s.urlEndValue()
		return i + 1
	}
	s.valueSeen = true
	s.valueSpace = s.valueSpace && isASCIISpace(ch)
	s.urlEatByte(ch)
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
		s.urlEndValue()
	default:
		s.valueSeen = true
		s.valueSpace = false
		s.urlEatByte(ch)
	}
	return i + 1
}

// beginQuotedValue resets value tracking when a quoted value opens.
func (s *scanner) beginQuotedValue() {
	s.valueSeen = false
	s.valueSpace = true
	s.urlStartValue()
}

// urlStartValue begins tracking the value of a URL attribute. Tracking is what
// lets scheme detection span the full rendered prefix instead of one value.
func (s *scanner) urlStartValue() {
	s.urlMatched = false
	s.urlPrefix = s.urlPrefix[:0]
	s.urlChecking = s.curURL
}

// urlEndValue stops tracking when the current attribute value is finished.
func (s *scanner) urlEndValue() {
	s.urlChecking = false
	s.urlMatched = false
	s.urlPrefix = s.urlPrefix[:0]
}

// urlEatByte folds one literal byte into the running URL prefix and advances
// the verdict: a completed dangerous scheme latches urlMatched; once the prefix
// can no longer be a dangerous scheme, tracking stops.
func (s *scanner) urlEatByte(ch byte) {
	if !s.urlChecking || isASCIISpace(ch) {
		return
	}
	if len(s.urlPrefix) < maxDangerousPrefixLen {
		s.urlPrefix = append(s.urlPrefix, lowerASCII(ch))
	}
	matched, possible := urlPrefixVerdict(s.urlPrefix)
	switch {
	case matched:
		// The scheme is fully present in literal text; reject at the first
		// interpolation that contributes to this value.
		s.urlChecking = false
		s.urlMatched = true
	case !possible:
		s.urlChecking = false
	}
}

// feedInterpolation marks that a value was emitted at the current position and
// validates the URL attribute value against its whole rendered prefix. The
// previous implementation inspected only this one value, so a dangerous scheme
// split across literals/interpolations ("java"+"script:") slipped through; the
// check now folds the value into the prefix accumulated since value start.
func (s *scanner) feedInterpolation(value string) *DangerousURLError {
	switch s.st {
	case stAttrEq:
		s.st = stAttrUnquoted
		s.valueSeen = value != ""
		s.valueSpace = allASCIISpace(value)
		s.urlStartValue()
	case stAttrDouble, stAttrSingle, stAttrUnquoted:
		if value != "" {
			s.valueSeen = true
		}
		s.valueSpace = s.valueSpace && allASCIISpace(value)
	}
	if s.urlMatched {
		return &DangerousURLError{Value: value}
	}
	if !s.urlChecking {
		return nil
	}
	s.urlPrefix = urlFold(s.urlPrefix, value)
	matched, possible := urlPrefixVerdict(s.urlPrefix)
	if matched {
		s.urlChecking = false
		s.urlMatched = true
		return &DangerousURLError{Value: value}
	}
	if !possible {
		s.urlChecking = false
	}
	return nil
}
