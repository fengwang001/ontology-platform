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
	// urlStart marks that, within the value, only whitespace has been seen so
	// far, so URL scheme validation applies to the next value.
	urlStart bool
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
	// valueSeen tracks whether an attribute value has begun; valueSpace tracks
	// whether only whitespace has been seen in that value so far.
	valueSeen  bool
	valueSpace bool

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
		return context{kind: ctxAttrDouble, urlAttr: s.curURL, urlStart: !s.valueSeen || s.valueSpace}, true
	case stAttrSingle:
		return context{kind: ctxAttrSingle, urlAttr: s.curURL, urlStart: !s.valueSeen || s.valueSpace}, true
	case stAttrUnquoted:
		return context{kind: ctxAttrUnquoted, urlAttr: s.curURL, urlStart: !s.valueSeen || s.valueSpace}, true
	case stAttrEq:
		// An interpolation directly after '=' begins an unquoted value.
		return context{kind: ctxAttrUnquoted, urlAttr: s.curURL, urlStart: true}, true
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
	s.valueSeen = false
	s.valueSpace = false
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
