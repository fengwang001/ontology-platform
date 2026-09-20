package ctxtmpl

import "strings"

// consumeTag advances one byte while inside a tag region.
func (sc *scanner) consumeTag(seg string, i int) int {
	c := seg[i]
	switch sc.state {
	case stTagName:
		if isSpace(c) {
			sc.state = stTagGap
		} else if c == '>' {
			sc.state = stText
		} else if c == '/' {
			// void-tag slash; stay, next whitespace or '>' leaves
		}
	case stTagGap:
		switch {
		case c == '>':
			sc.state = stText
		case c == '/' || isSpace(c):
			// stay
		case c == '=':
			sc.urlAttr = false
			sc.state = stAttrNameGap
		default:
			sc.startAttr(c)
		}
	case stAttrName:
		switch {
		case c == '=':
			sc.bindAttrName()
			sc.state = stAfterEq
		case isSpace(c):
			sc.bindAttrName()
			sc.state = stAttrNameGap
		case c == '>':
			sc.bindAttrName()
			sc.state = stText
		case c == '/':
		default:
			sc.attrName = append(sc.attrName, c)
		}
	case stAttrNameGap:
		switch {
		case c == '=':
			sc.state = stAfterEq
		case c == '>':
			sc.state = stText
		case c == '/' || isSpace(c):
			// stay
		default:
			sc.startAttr(c)
		}
	case stAfterEq:
		if isSpace(c) {
			break
		}
		sc.enterValue(c)
	case stValDouble:
		if c == '"' {
			sc.state = stTagGap
		} else if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			sc.valStart = true
		}
	case stValSingle:
		if c == '\'' {
			sc.state = stTagGap
		} else if !isSpace(c) {
			sc.valStart = true
		}
	case stValUnquoted:
		switch {
		case c == '>':
			sc.state = stText
		case isSpace(c):
			sc.state = stTagGap
		default:
			sc.valStart = true
		}
	}
	return i
}

func (sc *scanner) startAttr(c byte) {
	sc.attrName = sc.attrName[:0]
	sc.attrName = append(sc.attrName, c)
	sc.urlAttr = false
	sc.state = stAttrName
}

func (sc *scanner) bindAttrName() {
	name := strings.ToLower(string(sc.attrName))
	sc.urlAttr = isURLAttr(name)
}

func (sc *scanner) enterValue(c byte) {
	sc.valStart = !isSpace(c)
	switch c {
	case '"':
		sc.state = stValDouble
		sc.valStart = false
	case '\'':
		sc.state = stValSingle
		sc.valStart = false
	default:
		sc.state = stValUnquoted
	}
}

// atInterpolation returns the context applying at the current position.
func (sc *scanner) atInterpolation() (interpolationContext, error) {
	if err := sc.openInterpolation(); err != nil {
		return interpolationContext{}, err
	}
	switch sc.state {
	case stText:
		return interpolationContext{kind: ctxText}, nil
	case stComment:
		return interpolationContext{kind: ctxComment}, nil
	case stValDouble:
		return interpolationContext{kind: ctxAttrDouble, urlValue: sc.urlAttr, atURLStart: !sc.valStart}, nil
	case stValSingle:
		return interpolationContext{kind: ctxAttrSingle, urlValue: sc.urlAttr, atURLStart: !sc.valStart}, nil
	case stValUnquoted:
		return interpolationContext{kind: ctxAttrUnquoted, urlValue: sc.urlAttr, atURLStart: !sc.valStart}, nil
	case stAfterEq:
		return interpolationContext{kind: ctxAttrUnquoted, urlValue: sc.urlAttr, atURLStart: true}, nil
	default:
		return interpolationContext{}, ErrInterpolationInTagName
	}
}

// afterInterpolation updates parser state once a value was inserted.
func (sc *scanner) afterInterpolation() {
	switch sc.state {
	case stValDouble, stValSingle, stValUnquoted:
		sc.valStart = true
	case stAfterEq:
		sc.state = stValUnquoted
		sc.valStart = true
	}
}
