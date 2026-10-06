package refupdate

import "strings"

// Separator is the hierarchical path separator inside reference names.
const Separator = "/"

// Namespace prefixes for the two legal reference namespaces.
const (
	BranchPrefix = "refs/heads/"
	TagPrefix    = "refs/tags/"
)

// RefKind identifies the namespace of a reference name.
type RefKind int

const (
	KindUnknown RefKind = iota
	KindBranch
	KindTag
)

func (k RefKind) String() string {
	switch k {
	case KindBranch:
		return "branch"
	case KindTag:
		return "tag"
	default:
		return "unknown"
	}
}

// RefName is a hierarchical reference name such as "refs/heads/main".
type RefName string

// Segments splits a reference name on the separator.
func (r RefName) Segments() []string {
	return strings.Split(string(r), Separator)
}

// Kind reports whether the name lives in the branch or tag namespace.
// Names outside both namespaces are KindUnknown (parameter-illegal).
func (r RefName) Kind() RefKind {
	switch {
	case strings.HasPrefix(string(r), BranchPrefix) && len(r) > len(BranchPrefix):
		return KindBranch
	case strings.HasPrefix(string(r), TagPrefix) && len(r) > len(TagPrefix):
		return KindTag
	default:
		return KindUnknown
	}
}

// ValidSyntax reports whether the name obeys the lexical rules.
// Namespace membership is checked separately via Kind.
func (r RefName) ValidSyntax() bool {
	s := string(r)
	if s == "" {
		return false
	}
	if strings.HasPrefix(s, Separator) || strings.HasSuffix(s, Separator) {
		return false
	}
	if strings.Contains(s, Separator+Separator) {
		return false
	}
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	for _, seg := range r.Segments() {
		if seg == "" || strings.HasPrefix(seg, ".") || strings.HasSuffix(seg, ".lock") {
			return false
		}
	}
	return true
}

// IsAncestorPrefix reports whether a is an ancestor segment prefix of b:
// a equals b plus a separator followed by a non-empty suffix.
func IsAncestorPrefix(a, b RefName) bool {
	return len(b) > len(a) &&
		strings.HasPrefix(string(b), string(a)+Separator)
}

// RefPrefixConflict reports whether the two names cannot coexist because one
// is an ancestor segment prefix of the other.
func RefPrefixConflict(a, b RefName) bool {
	return a != b && (IsAncestorPrefix(a, b) || IsAncestorPrefix(b, a))
}
