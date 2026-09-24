// Package precond decides the outcome of a conditional request.
//
// Evaluation order (RFC 7232 §6): If-Match, else If-Unmodified-Since;
// then If-None-Match, else If-Modified-Since. A header that fails to
// parse is ignored, never fatal. The clock is consulted at most once,
// only when a date header is present and parses (future dates are
// ignored); with no conditional headers it is never called.
package precond

import (
	"strings"
	"time"

	"ontology/etag"
	"ontology/httpdate"
)

// Result is the verdict for a conditional request.
type Result int

const (
	Continue    Result = iota // proceed with the request
	NotModified               // 304: cached representation is current
	Failed                    // 412: precondition failed
)

func (r Result) String() string {
	return [...]string{"Continue", "NotModified", "Failed"}[r]
}

// Request carries the raw conditional header values; "" means absent.
type Request struct {
	Method            string
	IfMatch           string
	IfNoneMatch       string
	IfModifiedSince   string
	IfUnmodifiedSince string
}

// Resource is the current state of the target resource.
type Resource struct {
	Exists       bool
	ETag         string
	LastModified time.Time
}

// Evaluate decides the outcome. It is pure: inputs are never mutated
// and repeated calls with the same inputs agree.
func Evaluate(q Request, r Resource, now func() time.Time) Result {
	if q.IfMatch != "" {
		if m, ok := match(q.IfMatch, r, etag.Strong); ok && !m {
			return Failed
		}
	} else if t, ok := date(q.IfUnmodifiedSince, now); ok && r.LastModified.After(t) {
		return Failed
	}
	if q.IfNoneMatch != "" {
		if m, ok := match(q.IfNoneMatch, r, etag.Weak); ok && m {
			if q.Method == "GET" || q.Method == "HEAD" {
				return NotModified
			}
			return Failed
		}
	} else if t, ok := date(q.IfModifiedSince, now); ok && !r.LastModified.After(t) {
		return NotModified
	}
	return Continue
}

// match reports whether the header matches; ok is false when the
// header is unparsable and must be ignored.
func match(h string, r Resource, cmp func(etag.Tag, etag.Tag) bool) (bool, bool) {
	if strings.TrimSpace(h) == "*" {
		return r.Exists, true
	}
	list, err := etag.ParseList(h)
	if err != nil {
		return false, false
	}
	cur, err := etag.Parse(r.ETag)
	if err != nil || !r.Exists {
		return false, true
	}
	for _, t := range list {
		if cmp(t, cur) {
			return true, true
		}
	}
	return false, true
}

func date(h string, now func() time.Time) (time.Time, bool) {
	if h == "" {
		return time.Time{}, false
	}
	t, err := httpdate.Parse(h)
	if err != nil || t.After(now()) {
		return time.Time{}, false
	}
	return t, true
}
