package canon

import (
	"errors"
	"strings"
)

// ErrInvalid rejects structurally malformed URLs.
var ErrInvalid = errors.New("canon: malformed URL")

// rawURL is the structural split of an absolute URL with authority.
type rawURL struct {
	scheme    string // lowercased
	schemeRaw string // as written
	authority string
	path      string
	query     string
	hasQuery  bool
	fragment  bool // a '#fragment' was present (and dropped)
}

// splitURL cuts raw into components without decoding anything.
func splitURL(raw string) (rawURL, error) {
	var u rawURL
	i := strings.IndexByte(raw, ':')
	if i <= 0 || !validScheme(raw[:i]) {
		return u, ErrInvalid
	}
	u.schemeRaw = raw[:i]
	u.scheme = strings.ToLower(u.schemeRaw)
	rest := raw[i+1:]
	if !strings.HasPrefix(rest, "//") {
		return u, ErrInvalid
	}
	rest = rest[2:]
	if j := strings.IndexByte(rest, '#'); j >= 0 {
		u.fragment = true
		rest = rest[:j]
	}
	if j := strings.IndexByte(rest, '?'); j >= 0 {
		u.hasQuery = true
		u.query = rest[j+1:]
		rest = rest[:j]
	}
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		u.authority, u.path = rest[:j], rest[j:]
	} else {
		u.authority = rest
	}
	return u, nil
}

func validScheme(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}
