package ctxtmpl

// isURLAttr reports whether name (already lowercased) is an attribute
// whose value is interpreted as a URL.
func isURLAttr(name string) bool {
	switch name {
	case "href", "src", "action", "formaction":
		return true
	}
	return false
}

// isDangerousURL reports whether s, used at the very start of a URL
// attribute value, begins with a dangerous scheme. The check is
// case-insensitive and ignores all whitespace, so obfuscations like
// "java\tscript:" or a leading "  DATA:" are still caught.
func isDangerousURL(s string) bool {
	compact := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if isSpace(c) {
			continue
		}
		compact = append(compact, lower(c))
	}
	return hasPrefix(compact, "javascript:") ||
		hasPrefix(compact, "vbscript:") ||
		hasPrefix(compact, "data:")
}

func hasPrefix(s []byte, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if s[i] != prefix[i] {
			return false
		}
	}
	return true
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}

func isAllSpace(s string) bool {
	for i := 0; i < len(s); i++ {
		if !isSpace(s[i]) {
			return false
		}
	}
	return true
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
