package scope

import "strings"

// Match reports whether ref matches a branch pattern.
// A trailing '*' means prefix match (a lone "*" matches everything);
// otherwise the match is exact equality.
func Match(pattern, ref string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(ref, pattern[:len(pattern)-1])
	}
	return pattern == ref
}

// ValidPattern reports whether pattern is a legal branch pattern.
// It must be non-empty, and '*' may occur only as the final byte.
func ValidPattern(pattern string) bool {
	if pattern == "" {
		return false
	}
	star := strings.IndexByte(pattern, '*')
	return star == -1 || star == len(pattern)-1
}

// MatchAny reports whether ref matches at least one pattern.
// An empty pattern list matches everything.
func MatchAny(patterns []string, ref string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if Match(pattern, ref) {
			return true
		}
	}
	return false
}
