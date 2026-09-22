package segpath

import (
	"strings"

	"ontology/pct"
)

// Path is a normalized path expressed as segments plus an explicit trailing
// slash flag. Empty strings in Segments are real empty segments (from
// consecutive slashes) and must be preserved: they are distinct from "/.".
type Path struct {
	Segments     []string
	TrailingSlash bool
	// Present is false when the URL had no path component at all.
	Present bool
	// Absolute is true when the path began with '/'.
	Absolute bool
}

// Result includes scanning metadata so callers can prove linear work.
type Result struct {
	Path    Path
	Changed bool
	Scanned int
}

// Normalize percent-normalizes raw and resolves "." / ".." dot segments.
// Each raw segment is percent-normalized exactly once.
//
// Rules (derived from RFC 3986 section 5.2.4, applied to the hierarchical
// path directly so empty segments survive):
//   - "." is dropped;
//   - ".." pops one non-dot segment when possible; above the root it is
//     swallowed (absolute paths cannot escape their root);
//   - a trailing slash is preserved as TrailingSlash, making "/a" and
//     "/a/" distinct;
//   - empty segments are kept, so "//" differs from "/".
func Normalize(raw string, present bool) (Result, error) {
	original := raw
	trailing := len(raw) > 0 && raw[len(raw)-1] == '/'
	absolute := strings.HasPrefix(raw, "/")
	segs := splitSegments(raw)
	if trailing && len(segs) > 0 && segs[len(segs)-1] == "" {
		segs = segs[:len(segs)-1]
	}
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		norm, _, _, err := pct.Normalize(seg)
		if err != nil {
			return Result{}, err
		}
		switch norm {
		case ".":
			continue
		case "..":
			// Pop one ordinary segment. An empty segment marks a
			// boundary and cannot be navigated "above".
			if n := len(out); n > 0 && out[n-1] != "" && out[n-1] != ".." {
				out = out[:n-1]
			}
			continue
		}
		out = append(out, norm)
	}
	return Result{
		Path:    Path{Segments: out, TrailingSlash: trailing, Present: present, Absolute: absolute},
		Changed: joinPath(out, trailing, absolute) != original,
		Scanned: len(original),
	}, nil
}

// Join renders a path; authority-bearing URLs always produce an absolute path.
func Join(p Path) string {
	if !p.Present {
		return ""
	}
	return joinPath(p.Segments, p.TrailingSlash, p.Absolute)
}

func joinPath(segs []string, trailing, absolute bool) string {
	var b []byte
	if absolute {
		b = append(b, '/')
	}
	b = append(b, strings.Join(segs, "/")...)
	if trailing && (len(segs) > 0 || absolute) {
		b = append(b, '/')
	}
	return string(b)
}

// splitSegments returns the contents between slashes; "" is a real empty
// segment. The empty string yields no segments; "/" yields one empty segment.
func splitSegments(s string) []string {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "/") {
		s = s[1:]
	}
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "/")
}
