package initsession

// IsValidIdentifier reports whether s is a non-empty identifier.
func IsValidIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		var ok bool
		switch {
		case c == '_':
			ok = true
		case c >= 'a' && c <= 'z':
			ok = true
		case c >= 'A' && c <= 'Z':
			ok = true
		case c >= '0' && c <= '9':
			ok = i > 0
		}
		if !ok {
			return false
		}
	}
	return true
}

// IsBlank reports whether s is the blank identifier "_".
func IsBlank(s string) bool {
	return s == blank
}

const blank = "_"
