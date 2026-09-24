// Package etag parses HTTP entity-tags and performs strong/weak comparison.
// See RFC 7232, section 2.3. It depends on no other project package.
package etag

import (
	"errors"
	"strings"
)

var ErrSyntax = errors.New("etag: invalid entity-tag syntax")

// Tag is a parsed entity-tag. Weak means the W/ weakness indicator was present.
type Tag struct {
	Weak   bool
	Opaque string
}

// Parse parses one entity-tag such as `"abc"` or `W/"abc"`.
func Parse(s string) (Tag, error) {
	rest := s
	if strings.HasPrefix(rest, "W/") {
		rest = rest[2:]
	} else if strings.HasPrefix(rest, "w/") {
		return Tag{}, ErrSyntax
	}
	if len(rest) < 2 || rest[0] != '"' || rest[len(rest)-1] != '"' {
		return Tag{}, ErrSyntax
	}
	body := rest[1 : len(rest)-1]
	opaque, err := decodeQuoted(body)
	if err != nil {
		return Tag{}, err
	}
	return Tag{Weak: strings.HasPrefix(s, "W/"), Opaque: opaque}, nil
}

func decodeQuoted(body string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == 0x21 || c >= 0x23 && c <= 0x5b || c >= 0x5d && c <= 0x7e:
			b.WriteByte(c)
		case c == '\\':
			i++
			if i >= len(body) {
				return "", ErrSyntax
			}
			esc := body[i]
			if esc == 0x09 || esc == 0x20 || esc >= 0x21 && esc <= 0x7e || esc >= 0x80 {
				b.WriteByte(esc)
			} else {
				return "", ErrSyntax
			}
		default:
			return "", ErrSyntax
		}
	}
	return b.String(), nil
}

// ParseList parses a comma-separated list, unfolding CRLF/CR/LF folding
// whitespace first. "*" is reported as the wildcard flag rather than tags.
func ParseList(s string) (tags []Tag, wildcard bool, err error) {
	s = unfold(s)
	parts := strings.Split(s, ",")
	if len(parts) == 1 && strings.TrimSpace(parts[0]) == "" {
		return nil, false, ErrSyntax
	}
	for _, item := range parts {
		item = strings.TrimSpace(item)
		if item == "" {
			return nil, false, ErrSyntax
		}
		if item == "*" {
			if len(parts) != 1 {
				return nil, false, ErrSyntax
			}
			return nil, true, nil
		}
		t, err := Parse(item)
		if err != nil {
			return nil, false, err
		}
		tags = append(tags, t)
	}
	return tags, false, nil
}

func unfold(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	return strings.Join(lines, " ")
}

// StrongEqual performs the strong comparison function: both tags must be
// strong and their opaque values must match.
func StrongEqual(a, b Tag) bool {
	return !a.Weak && !b.Weak && a.Opaque == b.Opaque
}

// WeakEqual performs the weak comparison function: opaque values match,
// regardless of either tag's weakness indicator.
func WeakEqual(a, b Tag) bool { return a.Opaque == b.Opaque }

// StrongAny reports whether t strongly matches any candidate.
func StrongAny(t Tag, list []Tag) bool {
	for _, c := range list {
		if StrongEqual(t, c) {
			return true
		}
	}
	return false
}

// WeakAny reports whether t weakly matches any candidate.
func WeakAny(t Tag, list []Tag) bool {
	for _, c := range list {
		if WeakEqual(t, c) {
			return true
		}
	}
	return false
}
