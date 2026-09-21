// Package slashpath implements normalization of slash-separated paths.
//
// The only separator is '/'. The package never touches the filesystem
// and treats every path as a pure string of '/'-separated segments.
//
// The implementation guarantees three invariants for any input p:
//
//  1. Idempotence: Clean(Clean(p)) == Clean(p).
//  2. Absoluteness is preserved: IsAbs(p) == IsAbs(Clean(p)).
//  3. Absolute paths never escape the root: if IsAbs(p), Clean(p)
//     contains no ".." segment.
package slashpath

import "strings"

// Clean returns the normalized form of p. See the package doc for the
// invariants it guarantees. It never fails and never panics.
//
// The result never contains consecutive '/', never has a trailing '/'
// (except the root "/" itself) and never contains a "." segment
// (except when the whole result is "."). A relative result may keep
// leading ".." segments; an absolute result never contains "..".
//
// Splitting on the byte '/' is safe for multibyte UTF-8 segments
// because '/' (0x2F) never occurs inside a multibyte encoding.
func Clean(p string) string {
	abs := IsAbs(p)

	var stack []string
	rest := p
	for {
		seg, tail, found := strings.Cut(rest, "/")
		rest = tail
		switch seg {
		case "", ".":
			// Drop empty segments (runs of '/', leading/trailing '/')
			// and no-op "." segments.
		case "..":
			if n := len(stack); n > 0 && stack[n-1] != ".." {
				stack = stack[:n-1]
			} else if !abs {
				// Relative paths keep leading ".." segments; they are
				// the only way to express "parent directory".
				stack = append(stack, seg)
			}
			// For absolute paths a ".." at the root is dropped, so an
			// absolute result can never escape the root.
		default:
			stack = append(stack, seg)
		}
		if !found {
			break
		}
	}

	joined := strings.Join(stack, "/")
	if abs {
		return "/" + joined
	}
	if joined == "" {
		return "."
	}
	return joined
}

// Join concatenates elems with '/', ignoring empty elements, and
// returns the Clean of the result.
func Join(elems ...string) string {
	nonEmpty := make([]string, 0, len(elems))
	for _, e := range elems {
		if e != "" {
			nonEmpty = append(nonEmpty, e)
		}
	}
	return Clean(strings.Join(nonEmpty, "/"))
}

// IsAbs reports whether p is absolute, i.e. starts with '/'.
func IsAbs(p string) bool {
	return strings.HasPrefix(p, "/")
}
