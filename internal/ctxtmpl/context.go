package ctxtmpl

// contextKind identifies the HTML parsing state at an interpolation point.
type contextKind int

const (
	// ctxText is ordinary HTML text between tags/comments.
	ctxText contextKind = iota
	// ctxAttrDouble is inside a double-quoted attribute value.
	ctxAttrDouble
	// ctxAttrSingle is inside a single-quoted attribute value.
	ctxAttrSingle
	// ctxAttrUnquoted is inside an unquoted attribute value.
	ctxAttrUnquoted
	// ctxComment is inside an HTML comment body.
	ctxComment
)

// context describes how an interpolated value must be escaped.
type context struct {
	kind contextKind
	// urlAttr marks an attribute whose name is href/src/action/formaction.
	urlAttr bool
}

// topState is the parser state outside per-attribute sub-states.
type topState int

const (
	stText         topState = iota // ordinary document text
	stComment                      // inside <!-- ... -->
	stTagName                      // right after '<', reading tag name
	stTagGap                       // whitespace inside a tag, between tokens
	stAttrName                     // reading an attribute name
	stAttrEq                       // saw '=' before the value
	stAttrDouble                   // inside "..."
	stAttrSingle                   // inside '...'
	stAttrUnquoted                 // inside an unquoted value
)

// scanner tracks the HTML structural state while scanning a template. Values
// emitted for interpolation are not fed back to the state machine; instead
// parser state is advanced conservatively as if a safe value had been emitted.
type scanner struct {
	st topState

	// nameBuf collects the current attribute name (lowercased ascii).
	nameBuf []byte
	// curURL is true while the attribute currently being parsed has a URL name.
	curURL bool
	// valuePrefix accumulates the raw content of the current URL attribute
	// value (literal template bytes and interpolated values) so dangerous
	// scheme detection can inspect the full prefix rendered so far, not just
	// a single interpolation. Only maintained while curURL is true.
	valuePrefix []byte

	// pendingInterp is set by emitInterpolation so the next literal scan knows
	// how to treat value whitespace conservatively.
	interpWasAtValueStart bool
}

// currentContext builds the escape context for an interpolation at this point.
func (s *scanner) currentContext() (context, bool) {
	switch s.st {
	case stText:
		return context{kind: ctxText}, true
	case stComment:
		return context{kind: ctxComment}, true
	case stAttrDouble:
		return context{kind: ctxAttrDouble, urlAttr: s.curURL}, true
	case stAttrSingle:
		return context{kind: ctxAttrSingle, urlAttr: s.curURL}, true
	case stAttrUnquoted:
		return context{kind: ctxAttrUnquoted, urlAttr: s.curURL}, true
	case stAttrEq:
		// An interpolation directly after '=' begins an unquoted value.
		return context{kind: ctxAttrUnquoted, urlAttr: s.curURL}, true
	default:
		// Tag name, attribute name, gap or '=': structural position.
		return context{}, false
	}
}

// closeTag leaves tag parsing state and returns to document text.
func (s *scanner) closeTag() {
	s.st = stText
	s.nameBuf = s.nameBuf[:0]
	s.curURL = false
	s.valuePrefix = s.valuePrefix[:0]
}

// finish validates that no tag, quote or comment is open at end of template.
func (s *scanner) finish() *SyntaxError {
	switch s.st {
	case stComment:
		return s.syntaxErr(ErrUnclosedComment)
	case stTagName, stTagGap, stAttrName, stAttrEq, stAttrUnquoted:
		return s.syntaxErr(ErrUnclosedTag)
	case stAttrDouble, stAttrSingle:
		return s.syntaxErr(ErrUnclosedQuote)
	default:
		return nil
	}
}

func (s *scanner) syntaxErr(err error) *SyntaxError {
	return &SyntaxError{Err: err}
}
