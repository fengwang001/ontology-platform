// Package segpath normalizes URL paths: per-segment escape normalization
// plus RFC 3986 style dot-segment resolution. Empty segments (consecutive
// slashes) and trailing slashes are preserved; ".." above the root is
// swallowed, never allowed to escape.
package segpath

import (
	"strings"

	"ontology/pct"
)

// Info carries the normalized path plus what happened on the way.
type Info struct {
	Path     string   // normalized path
	Segments []string // normalized segments after dot resolution
	DotFired bool     // dot-segment resolution removed anything
	EscFired bool     // escape normalization changed any segment
}

// Normalize processes raw, which must be "" or start with '/'.
// Splitting happens on literal '/' only, so an escaped %2F stays inside
// its segment and never changes the segment structure.
func Normalize(raw string) (Info, error) {
	var info Info
	segs := strings.Split(raw, "/")
	absolute := strings.HasPrefix(raw, "/")
	out := make([]string, 0, len(segs))
	for i, s := range segs {
		if i == 0 && absolute {
			continue
		}
		ns, err := pct.Normalize(s)
		if err != nil {
			return Info{}, err
		}
		if ns != s {
			info.EscFired = true
		}
		last := i == len(segs)-1
		switch ns {
		case ".":
			info.DotFired = true
			if last {
				out = append(out, "")
			}
		case "..":
			info.DotFired = true
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
			if last {
				out = append(out, "")
			}
		default:
			out = append(out, ns)
		}
	}
	var b strings.Builder
	if absolute {
		b.WriteByte('/')
	}
	b.WriteString(strings.Join(out, "/"))
	info.Path = b.String()
	info.Segments = out
	return info, nil
}
