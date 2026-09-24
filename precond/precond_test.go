package precond_test

import (
	"errors"
	"testing"
	"time"

	"ontology/etag"
	"ontology/httpdate"
	"ontology/precond"
)

var t0 = time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)

const (
	d0     = "Thu, 24 Sep 2026 12:00:00 GMT"
	before = "Thu, 24 Sep 2026 11:00:00 GMT"
)

func res() precond.Resource { return precond.Resource{Exists: true, ETag: `"v"`, LastModified: t0} }

func ev(q precond.Request, r precond.Resource) precond.Result {
	return precond.Evaluate(q, r, func() time.Time { return t0.Add(time.Hour) })
}

func TestCompare(t *testing.T) {
	w, _ := etag.Parse(`W/"x"`)
	s, _ := etag.Parse(`"x"`)
	if !etag.Weak(w, s) || etag.Strong(w, s) || !etag.Weak(w, w) || etag.Strong(w, w) ||
		!etag.Weak(s, s) || !etag.Strong(s, s) {
		t.Error("strong/weak comparison combos")
	}
	if _, err := etag.ParseList(" , ,"); err == nil {
		t.Error("empty list must be a syntax error")
	}
}

func TestPrecond(t *testing.T) {
	gone := precond.Resource{LastModified: t0}
	cases := []struct {
		q    precond.Request
		r    precond.Resource
		want precond.Result
	}{
		{precond.Request{IfMatch: `"v"`, IfUnmodifiedSince: before}, res(), precond.Continue}, // If-Match suppresses IUS
		{precond.Request{IfMatch: "*"}, res(), precond.Continue},
		{precond.Request{IfMatch: "*"}, gone, precond.Failed},
		{precond.Request{IfNoneMatch: "*"}, res(), precond.NotModified},
		{precond.Request{IfNoneMatch: "*"}, gone, precond.Continue},
		{precond.Request{IfNoneMatch: "\"a\",\r\n \"v\", \"b\""}, res(), precond.NotModified}, // folded list
		{precond.Request{IfMatch: `"a", "v"`}, res(), precond.Continue},                       // any-match
		{precond.Request{IfNoneMatch: `W/"v"`}, res(), precond.NotModified},                   // weak cmp
		{precond.Request{IfNoneMatch: `"v"`}, res(), precond.NotModified},                     // read
		{precond.Request{Method: "PUT", IfNoneMatch: `"v"`}, res(), precond.Failed},           // write
		{precond.Request{IfModifiedSince: d0}, res(), precond.NotModified},                    // equal
		{precond.Request{IfUnmodifiedSince: d0}, res(), precond.Continue},                     // equal
		{precond.Request{IfModifiedSince: "junk", IfUnmodifiedSince: "junk"}, res(), precond.Continue},
	}
	for _, c := range cases {
		if c.q.Method == "" {
			c.q.Method = "GET"
		}
		if got := ev(c.q, c.r); got != c.want {
			t.Errorf("%+v: got %v want %v", c.q, got, c.want)
		}
	}
}

func TestDateErrors(t *testing.T) {
	cases := map[string]error{
		"Thu, 24 Sep 2026 12:00:00 UTC":  httpdate.ErrSuffix,
		"Thu, 24 Foo 2026 12:00:00 GMT":  httpdate.ErrMonth,
		"Mon, 30 Feb 2026 12:00:00 GMT":  httpdate.ErrDate,
		"Thu,  24 Sep 2026 12:00:00 GMT": httpdate.ErrSpace,
	}
	for in, want := range cases {
		if _, err := httpdate.Parse(in); !errors.Is(err, want) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestNoClockNoMutation(t *testing.T) {
	n := 0
	q, r := precond.Request{Method: "GET"}, res()
	precond.Evaluate(q, r, func() time.Time { n++; return t0 })
	if n != 0 || q != (precond.Request{Method: "GET"}) || r != res() {
		t.Error("clock called or inputs mutated")
	}
}

func TestMatrix(t *testing.T) {
	im := []string{"", `"v"`, `"z"`} // absent / match / mismatch
	ius := []string{"", d0, before}  // absent / pass / fail
	ims := []string{"", before, d0}  // absent / modified / unmodified
	for i := 0; i < 81; i++ {
		a, b, c, d := i/27, i/9%3, i/3%3, i%3
		q := precond.Request{Method: "GET", IfMatch: im[a],
			IfUnmodifiedSince: ius[b], IfNoneMatch: im[c], IfModifiedSince: ims[d]}
		if got := ev(q, res()); got != model(a, b, c, d) || got != ev(q, res()) {
			t.Errorf("%+v: got %v", q, got)
		}
	}
}

func model(a, b, c, d int) precond.Result {
	if a == 2 || (a == 0 && b == 2) {
		return precond.Failed
	}
	if c == 1 || (c == 0 && d == 2) {
		return precond.NotModified
	}
	return precond.Continue
}
