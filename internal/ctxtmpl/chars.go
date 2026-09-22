package ctxtmpl

func isASCIIAlpha(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

// asciiSpaceBytes is the single source of truth for what the renderer treats
// as whitespace. Both the template scanner (which decides where an unquoted
// attribute value ends) and the unquoted-attribute escaper (which decides
// which bytes must become entities) must derive their whitespace sets from
// this list: a byte that terminates an unquoted value in the scanner but is
// not escaped would let an interpolated value start a new attribute.
var asciiSpaceBytes = []byte{' ', '\t', '\n', '\r', '\f', '\v'}

func isASCIISpace(ch byte) bool {
	for _, sp := range asciiSpaceBytes {
		if ch == sp {
			return true
		}
	}
	return false
}

// asciiSpaceEntities maps every byte in asciiSpaceBytes to its numeric HTML
// entity. The unquoted-attribute escaper builds its whitespace rows from this
// map so the two whitespace sets can never drift apart again. Previously the
// scanner also treated '\f' and '\v' as value terminators while the escaper
// only encoded space/tab/LF/CR, so a value containing those two bytes escaped
// the unquoted attribute and injected a new attribute name.
var asciiSpaceEntities = map[byte]string{
	' ':  "&#32;",
	'\t': "&#9;",
	'\n': "&#10;",
	'\r': "&#13;",
	'\f': "&#12;",
	'\v': "&#11;",
}

func lowerASCII(ch byte) byte {
	if ch >= 'A' && ch <= 'Z' {
		return ch + ('a' - 'A')
	}
	return ch
}

// isURLAttr reports whether name is one of the URL-bearing attributes.
func isURLAttr(name []byte) bool {
	switch string(name) {
	case "href", "src", "action", "formaction":
		return true
	default:
		return false
	}
}
