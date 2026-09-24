// Package directive parses comma-separated Cache-Control directives such as
// "max-age=60, no-store". A directive is either a bare token ("no-store") or
// a token with a non-negative decimal-seconds argument ("max-age=60").
package directive

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

// ErrEmptyNoDate marks a response that carries no Cache-Control text and no
// usable Date header, so nothing can establish a freshness origin.
var ErrEmptyNoDate = errors.New("directive: empty control directives and no Date")

// MaxSeconds is used when a delta-seconds value overflows int64.
const MaxSeconds = int64(math.MaxInt64)

// Set holds the directives found in one Cache-Control header field value.
// First occurrence of a name wins; later duplicates are discarded.
type Set struct {
	flags map[string]bool  // present without a usable argument
	arg   map[string]int64 // present with a usable argument
}

// Parse splits text into directives. Malformed individual items are ignored,
// not the whole field. Names are case-insensitive and stored lower-cased.
func Parse(text string) (Set, error) {
	s := Set{flags: map[string]bool{}, arg: map[string]int64{}}
	for _, item := range strings.Split(text, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name := item
		var raw string
		hasArg := false
		if i := strings.IndexByte(item, '='); i >= 0 {
			name, raw, hasArg = item[:i], strings.TrimSpace(item[i+1:]), true
		}
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if _, seen := s.flags[name]; seen {
			continue
		}
		if _, seen := s.arg[name]; seen {
			continue
		}
		if !hasArg {
			s.flags[name] = true
			continue
		}
		if v, ok := parseDelta(raw); ok {
			s.arg[name] = v
		}
	}
	return s, nil
}

// parseDelta accepts only a non-negative decimal integer, optionally wrapped
// in double quotes. Signed, fractional or non-numeric values are rejected.
func parseDelta(raw string) (int64, bool) {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	if raw == "" {
		return 0, false
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if errors.Is(err, strconv.ErrRange) {
		return MaxSeconds, true
	}
	if err != nil {
		return 0, false
	}
	return v, true
}

// Delta returns the argument for name and whether a usable one was found.
func (s Set) Delta(name string) (int64, bool) {
	v, ok := s.arg[name]
	return v, ok
}

// Present reports bare presence even when the argument is a zero value.
func (s Set) Present(name string) bool {
	_, ok := s.arg[name]
	return ok || s.flags[name]
}
