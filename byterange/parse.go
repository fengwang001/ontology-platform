package byterange

import (
	"strconv"
	"strings"
)

// spec is one syntactically valid range-spec from the header,
// not yet resolved against the resource size.
type spec struct {
	start  int64 // first byte, valid when !suffix
	end    int64 // last byte, -1 means "to the end"
	suffix bool  // true: last `length` bytes of the resource
	length int64 // suffix length, valid when suffix
}

// Parse parses an HTTP Range header (e.g. "bytes=0-499, -100") and
// normalizes the requested ranges against the resource size in bytes.
//
// The returned ranges are absolute closed intervals, sorted by Start,
// non-overlapping and non-adjacent. It returns ErrMalformed for syntax
// errors and ErrUnsatisfiable when no range can be satisfied.
func Parse(header string, size int64) ([]Range, error) {
	if size < 0 {
		size = 0
	}
	specs, err := parseSpecs(header)
	if err != nil {
		return nil, err
	}
	ranges := resolveAll(specs, size)
	if len(ranges) == 0 {
		return nil, ErrUnsatisfiable
	}
	return merge(ranges), nil
}

// parseSpecs validates the header syntax and returns the raw specs.
func parseSpecs(header string) ([]spec, error) {
	unit, list, ok := strings.Cut(strings.TrimSpace(header), "=")
	if !ok || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return nil, ErrMalformed
	}
	parts := strings.Split(list, ",")
	specs := make([]spec, 0, len(parts))
	for _, p := range parts {
		s, err := parseSpec(strings.TrimSpace(p))
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	if len(specs) == 0 {
		return nil, ErrMalformed
	}
	return specs, nil
}

// parseSpec parses a single range-spec: "N-M", "N-" or "-N".
func parseSpec(p string) (spec, error) {
	first, last, ok := strings.Cut(p, "-")
	if !ok {
		return spec{}, ErrMalformed
	}
	if first == "" && last == "" {
		return spec{}, ErrMalformed // "-"
	}
	if first == "" { // suffix form "-N"
		n, err := parseNumber(last)
		if err != nil {
			return spec{}, err
		}
		return spec{suffix: true, length: n}, nil
	}
	start, err := parseNumber(first)
	if err != nil {
		return spec{}, err
	}
	if last == "" { // open-ended form "N-"
		return spec{start: start, end: -1}, nil
	}
	end, err := parseNumber(last)
	if err != nil {
		return spec{}, err
	}
	if start > end {
		return spec{}, ErrMalformed
	}
	return spec{start: start, end: end}, nil
}

// parseNumber parses a non-negative decimal bound, rejecting signs,
// empty strings and values that overflow int64.
func parseNumber(s string) (int64, error) {
	if s == "" {
		return 0, ErrMalformed
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrMalformed
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, ErrMalformed
	}
	return n, nil
}
