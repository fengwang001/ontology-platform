// Package segpath normalizes URL paths segment by segment: percent
// normalization per segment, dot-segment resolution, preserved empty
// segments and preserved trailing-slash semantics.
package segpath

import (
	"strings"

	"ontology/pct"
)

// Normalize splits raw into "/" separated segments, percent-normalizes each
// segment, resolves "." and ".." against a stack (".." above the root is
// swallowed, never escapes), and keeps empty segments (consecutive slashes)
// and the trailing slash. It returns the canonical path, the resolved
// segment list, and two flags: escChanged (percent normalization rewrote
// bytes) and dotsResolved (dot segments were resolved). Segments are
// decoded before dot resolution, so "%2E%2E" is treated as ".." (safe for
// authorization checks).
func Normalize(raw string) (path string, segs []string, escChanged, dotsResolved bool, err error) {
	parts := strings.Split(raw, "/")
	absolute := strings.HasPrefix(raw, "/")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		last := i == len(parts)-1
		norm, err := pct.Normalize(p)
		if err != nil {
			return "", nil, false, false, err
		}
		if norm != p {
			escChanged = true
		}
		switch norm {
		case ".":
			dotsResolved = true
			if last {
				out = append(out, "") // "/a/." -> "/a/"
			}
		case "..":
			dotsResolved = true
			if canPop(out, absolute) {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "") // "/a/.." -> "/"
			}
		default:
			out = append(out, norm)
		}
	}
	return strings.Join(out, "/"), out, escChanged, dotsResolved, nil
}

// canPop reports whether the stack may lose one segment: the leading empty
// segment of an absolute path is the root marker and must never be popped,
// which is exactly what swallows ".." above the root.
func canPop(out []string, absolute bool) bool {
	if len(out) == 0 {
		return false
	}
	if absolute && len(out) == 1 && out[0] == "" {
		return false
	}
	return true
}
