// Package key defines normalized variant identities, Vary matching and the
// single-flight coalescing key. It is free of locks and time.
package key

import (
	"sort"
	"strconv"
)

// Scope of a stored variant: public (shared across subjects) or a named
// private subject.
type Scope string

// PublicScope marks a variant shared across all subjects.
const PublicScope Scope = ""

// Identity identifies a stored variant: its ownership, the sorted set of Vary
// header names and the corresponding request-header value vector.
type Identity struct {
	Owner Scope
	Vary  []string
	Vals  []string
}

// CanonicalHeaderKey identifies a coalescing group: a path plus a header map
// with authorization and cookie removed.
type CanonicalHeaderKey struct {
	Path string
	Head map[string]string
}

// ValidateHeaders reports whether every header name is non-empty.
func ValidateHeaders(head map[string]string) bool {
	for name := range head {
		if name == "" {
			return false
		}
	}
	return true
}

// VariantIdentity builds a variant identity for a response. Missing header
// values read as the empty string, matching the Vary equality rule.
func VariantIdentity(owner Scope, vary []string, head map[string]string) Identity {
	names := NormalizeVary(vary)
	vals := make([]string, len(names))
	for i, name := range names {
		vals[i] = head[name]
	}
	return Identity{Owner: owner, Vary: names, Vals: vals}
}

// VaryMatches reports whether a request matches the Vary vector of an identity.
func VaryMatches(id Identity, head map[string]string) bool {
	for i, name := range id.Vary {
		if head[name] != id.Vals[i] {
			return false
		}
	}
	return true
}

// CoalescingKey strips authorization/cookie for single-flight grouping.
func CoalescingKey(path string, head map[string]string) CanonicalHeaderKey {
	stripped := make(map[string]string, len(head))
	for name, value := range head {
		if name == "authorization" || name == "cookie" {
			continue
		}
		stripped[name] = value
	}
	return CanonicalHeaderKey{Path: path, Head: stripped}
}

// NormalizeVary returns the sorted, de-duplicated Vary header names. The
// wildcard "*" is passed through verbatim (the caller rejects storage).
func NormalizeVary(vary []string) []string {
	if len(vary) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(vary))
	for _, name := range vary {
		seen[name] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Encode renders an identity as a collision-free deterministic string, usable
// as a map key. Ownership and every Vary name/value carry a length prefix.
func (id Identity) Encode() string {
	buf := make([]byte, 0, 64)
	buf = appendLen(buf, string(id.Owner))
	buf = append(buf, ';')
	for i, name := range id.Vary {
		buf = appendLen(buf, name)
		buf = append(buf, '=')
		buf = appendLen(buf, id.Vals[i])
		buf = append(buf, ',')
	}
	return string(buf)
}

// Equal reports whether two identities are identical.
func (id Identity) Equal(other Identity) bool {
	if id.Owner != other.Owner || len(id.Vary) != len(other.Vary) {
		return false
	}
	for i := range id.Vary {
		if id.Vary[i] != other.Vary[i] || id.Vals[i] != other.Vals[i] {
			return false
		}
	}
	return true
}

// Encode renders a coalescing key deterministically; the header map is
// serialized in sorted name order with length prefixes.
func (k CanonicalHeaderKey) Encode() string {
	names := make([]string, 0, len(k.Head))
	for name := range k.Head {
		names = append(names, name)
	}
	sort.Strings(names)
	buf := make([]byte, 0, 64)
	buf = appendLen(buf, k.Path)
	buf = append(buf, ';')
	for _, name := range names {
		buf = appendLen(buf, name)
		buf = append(buf, '=')
		buf = appendLen(buf, k.Head[name])
		buf = append(buf, ',')
	}
	return string(buf)
}

func appendLen(buf []byte, s string) []byte {
	buf = strconv.AppendInt(buf, int64(len(s)), 10)
	buf = append(buf, ':')
	buf = append(buf, s...)
	return buf
}
