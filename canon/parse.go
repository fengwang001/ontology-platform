package canon

import "strings"

type parsed struct {
	scheme    string
	hasAuth   bool
	authority string
	path      string
	query     string
	hasQuery  bool
}

// splitURL performs a structural split without consulting net/url:
// scheme://authority/path?query (#fragment is always dropped).
func splitURL(raw string) parsed {
	p := parsed{}
	s := raw
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	rest := s
	if i := strings.IndexByte(s, ':'); i >= 0 {
		scheme := s[:i]
		if validScheme(scheme) {
			p.scheme = strings.ToLower(scheme)
			rest = s[i+1:]
		}
	}
	if strings.HasPrefix(rest, "//") {
		p.hasAuth = true
		rest = rest[2:]
		end := len(rest)
		if i := strings.IndexAny(rest, "/?"); i >= 0 {
			end = i
		}
		p.authority = rest[:end]
		rest = rest[end:]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		p.hasQuery = true
		p.path = rest[:i]
		p.query = rest[i+1:]
	} else {
		p.path = rest
	}
	return p
}

func validScheme(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		case i > 0 && (c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

func defaultPortFor(scheme string) string {
	switch scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
