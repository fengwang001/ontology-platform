package eval

type tokenKind int

const (
	tokEOF tokenKind = iota
	tokNumber
	tokPlus
	tokMinus
	tokStar
	tokSlash
	tokLParen
	tokRParen
)

type token struct {
	kind tokenKind
	// pos is the 0-based byte offset of the token in the source.
	pos int
	// text holds the raw literal for tokNumber, otherwise the symbol.
	text string
}
