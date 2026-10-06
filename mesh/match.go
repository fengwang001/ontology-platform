package mesh

import "strings"

// stripQuery removes the query string before any path matching.
func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}

// segments splits a path on '/'. A single trailing slash produces no extra
// segment, so "/" and "/a" behave as whole segment chains: "/" -> [""] ,
// "/a/" -> ["", "a"], "/a/b" -> ["", "a", "b"].
func segments(path string) []string {
	s := strings.Split(path, "/")
	if len(s) > 1 && s[len(s)-1] == "" {
		s = s[:len(s)-1]
	}
	return s
}

// segmentPrefix reports whether want is a whole-segment prefix of got:
// want must equal the whole path or be the start of complete '/'-delimited
// segments. Half-segment matches ("/a" vs "/ab") return false.
func segmentPrefix(want, got string) bool {
	w := segments(want)
	g := segments(got)
	if len(w) > len(g) {
		return false
	}
	for i := range w {
		if w[i] != g[i] {
			return false
		}
	}
	return true
}

// pathMatches evaluates one path condition against a query-stripped path.
func pathMatches(cond *PathCondition, strippedPath string) bool {
	if cond == nil {
		return true
	}
	switch cond.Kind {
	case PathExact:
		return cond.Value == strippedPath
	case PathPrefix:
		return segmentPrefix(cond.Value, strippedPath)
	default:
		return false
	}
}

// headerValueSatisfies tests one concrete header value against a condition
// (values are case-sensitive).
func headerValueSatisfies(op HeaderOp, want, got string) bool {
	switch op {
	case HeaderExact:
		return got == want
	case HeaderPrefix:
		return strings.HasPrefix(got, want)
	case HeaderExists:
		return true
	default:
		return false
	}
}

// headerSatisfies tests all values carried under one header name: the
// condition holds when ANY value satisfies it.
func headerSatisfies(cond HeaderCondition, headers map[string][]string) bool {
	values, ok := headers[strings.ToLower(cond.Name)]
	if !ok {
		return false
	}
	for _, v := range values {
		if headerValueSatisfies(cond.Op, cond.Value, v) {
			return true
		}
	}
	return false
}

// matcherMatches evaluates a matcher: every header condition must hold
// (AND) while the single path condition is evaluated directly.
func matcherMatches(m Matcher, strippedPath string, headers map[string][]string) bool {
	if !pathMatches(m.Path, strippedPath) {
		return false
	}
	for i := range m.Headers {
		if !headerSatisfies(m.Headers[i], headers) {
			return false
		}
	}
	return true
}

// headerEntailed reports whether former condition h entails latter condition
// g, i.e. every request satisfying h also satisfies g. Header names are
// case-insensitive.
func headerEntailed(h, g HeaderCondition) bool {
	if !strings.EqualFold(h.Name, g.Name) {
		return false
	}
	switch h.Op {
	case HeaderExists:
		// Presence only entails presence.
		return g.Op == HeaderExists
	case HeaderExact:
		switch g.Op {
		case HeaderExists:
			return true
		case HeaderPrefix:
			return strings.HasPrefix(h.Value, g.Value)
		case HeaderExact:
			return h.Value == g.Value
		}
	case HeaderPrefix:
		switch g.Op {
		case HeaderExists:
			return true
		case HeaderPrefix:
			// A prefix entails strictly shorter (or equal) prefixes.
			return strings.HasPrefix(h.Value, g.Value)
		}
	}
	return false
}

// pathCovers reports whether the request set of former condition a covers
// the set of latter condition b. A nil condition matches every path.
func pathCovers(a, b *PathCondition) bool {
	if a == nil {
		return true
	}
	if b == nil {
		return false
	}
	switch a.Kind {
	case PathExact:
		// An exact condition covers only the identical exact condition.
		return b.Kind == PathExact && a.Value == b.Value
	case PathPrefix:
		// A prefix covers exact and prefix conditions located at or below
		// it on whole segment boundaries.
		switch b.Kind {
		case PathExact, PathPrefix:
			return segmentPrefix(a.Value, b.Value)
		}
	}
	return false
}

// matcherCovers reports whether every request matched by b is also matched
// by a (a covers b). On headers, EVERY condition of a must be entailed by
// SOME condition of b.
func matcherCovers(a, b Matcher) bool {
	if !pathCovers(a.Path, b.Path) {
		return false
	}
	for i := range a.Headers {
		found := false
		for j := range b.Headers {
			if headerEntailed(a.Headers[i], b.Headers[j]) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// canonicalizeHeaders lower-cases every header name once per request,
// preserving multi-values.
func canonicalizeHeaders(in map[string][]string) map[string][]string {
	if in == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(in))
	for name, vals := range in {
		out[strings.ToLower(name)] = vals
	}
	return out
}
