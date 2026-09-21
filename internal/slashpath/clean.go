// Package slashpath implements normalization of slash-separated paths.
//
// The only separator is '/'; Windows backslashes and the filesystem are
// not involved. All functions are pure: no errors, no panics, and every
// input has a deterministic output.
//
// The package guarantees three invariants:
//
//  1. Idempotence: Clean(Clean(p)) == Clean(p) for any p.
//  2. Absoluteness preservation: IsAbs(p) == IsAbs(Clean(p)).
//  3. No escaping the root: if IsAbs(p), Clean(p) contains no ".." segment.
package slashpath

import "strings"

// IsAbs reports whether p is an absolute path, i.e. starts with '/'.
func IsAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}

// Clean returns the canonical form of p:
//
//   - empty input becomes "."
//   - consecutive '/' collapse into one
//   - "." segments are dropped
//   - ".." cancels the previous normal segment when possible
//   - leading ".." of a relative path is kept (it is the only way to
//     express "parent directory" relative to an unknown base)
//   - ".." of an absolute path at the root is dropped, so an absolute
//     path can never escape the root
//   - the result never ends with '/' (except the root "/" itself)
//
// The result contains no consecutive '/', no trailing '/' (except "/"),
// and no "." segment (unless the whole result is ".").
func Clean(p string) string {
	if p == "" {
		return "."
	}
	abs := IsAbs(p)
	stack := make([]string, 0, strings.Count(p, "/")+1)
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "", ".":
			// Skip empty segments (consecutive or edge slashes) and
			// "." segments.
		case "..":
			if n := len(stack); n > 0 && stack[n-1] != ".." {
				stack = stack[:n-1]
			} else if !abs {
				// Relative path with nothing to cancel: keep the
				// leading ".." to preserve the meaning.
				stack = append(stack, "..")
			}
			// Absolute path at the root: ".." is dropped so the
			// result can never escape the root.
		default:
			stack = append(stack, seg)
		}
	}
	if len(stack) == 0 {
		if abs {
			return "/"
		}
		return "."
	}
	joined := strings.Join(stack, "/")
	if abs {
		return "/" + joined
	}
	return joined
}
