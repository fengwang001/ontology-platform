// Package segpath normalizes URL paths as ordered segments: "." / ".."
// resolution, preservation of empty segments and of a meaningful trailing
// slash, and segment-local percent normalization. It depends only on pct.
package segpath

import (
	"strconv"
	"strings"

	"ontology/pct"
)

// Path is a normalized URL path split at its "/" boundaries.
type Path struct {
	absolute bool
	segments []string
	trailing bool
}

// Error reports a malformed escape together with its byte offset.
type Error struct {
	Offset int
	Kind   string // "truncated", "badhex" or "utf8"
}

func (e *Error) Error() string {
	return "segpath: malformed escape (" + e.Kind + ") at byte " + strconv.Itoa(e.Offset)
}

// IsTruncated / IsBadHex / IsInvalidUTF8 classify the escape error.
func (e *Error) IsTruncated() bool   { return e.Kind == "truncated" }
func (e *Error) IsBadHex() bool      { return e.Kind == "badhex" }
func (e *Error) IsInvalidUTF8() bool { return e.Kind == "utf8" }

// Parse normalizes a URL path in a single left-to-right scan.
func Parse(raw string, scan pct.ByteScanner) (*Path, error) {
	if scan != nil {
		scan(len(raw))
	}
	p := &Path{absolute: strings.HasPrefix(raw, "/")}
	if raw == "/" {
		p.trailing = true
		return p, nil
	}
	rest := raw
	if p.absolute {
		rest = raw[1:]
	}
	if rest == "" {
		return p, nil
	}
	p.trailing = rest[len(rest)-1] == '/'
	segs, starts := splitSegments(rest, p.trailing, boolToInt(p.absolute))

	var stack []string
	var stackOff []int
	var stackStruct []bool
	for idx, s := range segs {
		switch s {
		case ".":
			continue
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
				stackOff = stackOff[:len(stackOff)-1]
				stackStruct = stackStruct[:len(stackStruct)-1]
			} else if !p.absolute {
				stack = append(stack, "..")
				stackOff = append(stackOff, starts[idx])
				stackStruct = append(stackStruct, true)
			}
			// Above an absolute root the ".." is swallowed, never an escape.
		default:
			stack = append(stack, s)
			stackOff = append(stackOff, starts[idx])
			stackStruct = append(stackStruct, false)
		}
	}

	out := make([]string, len(stack))
	for i, s := range stack {
		if stackStruct[i] {
			out[i] = s // a retained relative ".." renders literally
			continue
		}
		norm, err := pct.Normalize(s, segmentSafe, nil)
		if err != nil {
			return nil, mapError(err, stackOff[i])
		}
		// A normalized segment must not re-parse as "." / ".." on a second
		// pass: escape its dots so normalization stays idempotent.
		if norm == "." || norm == ".." {
			norm = escapeDots(norm)
		}
		out[i] = norm
	}
	p.segments = out
	return p, nil
}

func splitSegments(rest string, trailing bool, base int) ([]string, []int) {
	var segs []string
	var starts []int
	start := 0
	for i := 0; i <= len(rest); i++ {
		if i == len(rest) || rest[i] == '/' {
			if trailing && i == len(rest) {
				break // drop the empty segment implied by a trailing slash
			}
			segs = append(segs, rest[start:i])
			starts = append(starts, base+start)
			start = i + 1
		}
	}
	return segs, starts
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func escapeDots(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if c == '.' {
			b.WriteString("%2E")
		} else {
			b.WriteByte(c)
		}
	}
	return b.String()
}

func segmentSafe(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z', b >= 'A' && b <= 'Z', b >= '0' && b <= '9':
		return true
	case b >= 0x80:
		return true
	}
	return strings.ContainsRune("-._~!$&'()*+,;=:@", rune(b))
}

func mapError(err error, base int) error {
	e, ok := err.(*pct.EscapeError)
	if !ok {
		return &Error{Offset: base, Kind: "utf8"}
	}
	kind := "utf8"
	switch {
	case pct.IsTruncated(e):
		kind = "truncated"
	case pct.IsBadHex(e):
		kind = "badhex"
	}
	return &Error{Kind: kind, Offset: base + e.Offset}
}

// Segments returns a copy of the normalized path segments.
func (p *Path) Segments() []string { return append([]string(nil), p.segments...) }

// IsAbsolute reports whether the path began with "/".
func (p *Path) IsAbsolute() bool { return p.absolute }

// HasTrailingSlash reports whether the normalized path ends with "/".
func (p *Path) HasTrailingSlash() bool { return p.trailing }

// String renders the normalized path ("" means an authority's empty path).
func (p *Path) String() string {
	if !p.absolute && len(p.segments) == 0 && !p.trailing {
		return ""
	}
	var b strings.Builder
	if p.absolute && (len(p.segments) > 0 || !p.trailing) {
		b.WriteByte('/')
	}
	b.WriteString(strings.Join(p.segments, "/"))
	if p.trailing {
		b.WriteByte('/')
	}
	return b.String()
}
