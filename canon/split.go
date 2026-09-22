package canon

import (
	"strings"

	"ontology/host"
	"ontology/query"
)

// splitScheme extracts and lowercases the scheme, reporting whether the
// case fold changed anything. Fragments and userinfo are rejected
// outright: silently dropping or misreading them would change the
// resource identity.
func splitScheme(raw string) (scheme, rest string, caseChanged bool, err error) {
	if strings.ContainsRune(raw, '#') {
		return "", "", false, &SyntaxError{Msg: "fragments (#) are not supported"}
	}
	i := strings.Index(raw, "://")
	if i <= 0 {
		return "", "", false, &SyntaxError{Msg: "missing scheme://"}
	}
	s := raw[:i]
	for j := 0; j < len(s); j++ {
		c := s[j]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' ||
			j > 0 && (c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.')
		if !ok {
			return "", "", false, &SyntaxError{Msg: "invalid scheme " + s}
		}
	}
	lower := strings.ToLower(s)
	return lower, raw[i+3:], lower != s, nil
}

// splitRest splits "authority/path?query" into its three parts.
func splitRest(rest string) (authority, path, rawQuery string, hasQuery bool, err error) {
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rawQuery, hasQuery = rest[i+1:], true
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		authority, path = rest[:i], rest[i:]
	} else {
		authority = rest
	}
	if strings.ContainsRune(authority, '@') {
		return "", "", "", false, &SyntaxError{Msg: "userinfo (@) is not supported"}
	}
	return authority, path, rawQuery, hasQuery, nil
}

// assemble builds the canonical string and the read-only Result.
func (n *Normalizer) assemble(raw, scheme string, schemeCaseChanged bool, h, port, cpath string,
	segs []string, cq string, items []query.Item, hasQuery bool, rw Rewrites,
	hch host.Changes, escChanged, dotsResolved bool) Result {
	if hch.Case || schemeCaseChanged {
		rw |= RwCase
	}
	if hch.TrailingDot {
		rw |= RwTrailingDot
	}
	if hch.DefaultPort {
		rw |= RwDefaultPort
	}
	if hch.PortSyntax {
		rw |= RwPortSyntax
	}
	if hch.IPv6 {
		rw |= RwIPv6
	}
	if escChanged {
		rw |= RwEscape
	}
	if dotsResolved {
		rw |= RwDotSegments
	}
	var b strings.Builder
	b.Grow(len(raw))
	b.WriteString(scheme)
	b.WriteString("://")
	b.WriteString(h)
	if port != "" {
		b.WriteByte(':')
		b.WriteString(port)
	}
	b.WriteString(cpath)
	if cq != "" {
		b.WriteByte('?')
		b.WriteString(cq)
	} else if hasQuery {
		rw |= RwEmptyQuery
	}
	canonical := b.String()
	return Result{
		Canonical:    canonical,
		Scheme:       scheme,
		Host:         h,
		Port:         port,
		Path:         cpath,
		PathSegments: segs,
		Query:        items,
		Rewritten:    canonical != raw,
		RewriteKinds: rw,
	}
}
