package ctxtmpl

func isASCIIAlpha(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z'
}

func isASCIISpace(ch byte) bool {
	return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' || ch == '\v'
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
