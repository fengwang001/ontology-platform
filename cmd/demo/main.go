// Command demo exercises the conditional-request evaluator end to end.
package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/etag"
	"ontology/httpdate"
	"ontology/precond"
)

var failed int

func check(name string, ok bool) {
	status := "OK  "
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Println(status, name)
}

func main() {
	w, _ := etag.Parse(`W/"x"`)
	s, _ := etag.Parse(`"x"`)
	w2, _ := etag.Parse(`W/"x"`)
	check(`strong: W/"x" vs "x" differ`, !etag.Strong(w, s))
	check(`strong: "x" vs "x" equal`, etag.Strong(s, s))
	check(`weak: W/"x" vs "x" equal`, etag.Weak(w, s))
	check(`weak: W/"x" vs W/"x" equal`, etag.Weak(w, w2))
	dateErrs := map[string]error{
		"Thu, 24 Sep 2026 12:00:00 UTC":  httpdate.ErrSuffix,
		"Thu, 24 Foo 2026 12:00:00 GMT":  httpdate.ErrMonth,
		"Mon, 30 Feb 2026 12:00:00 GMT":  httpdate.ErrDate,
		"Thu,  24 Sep 2026 12:00:00 GMT": httpdate.ErrSpace,
	}
	dateOK := true
	for in, want := range dateErrs {
		if _, err := httpdate.Parse(in); !errors.Is(err, want) {
			dateOK = false
		}
	}
	check("date errors: suffix/month/date/space distinct", dateOK)

	t0, _ := httpdate.Parse("Thu, 24 Sep 2026 12:00:00 GMT")
	const d0 = "Thu, 24 Sep 2026 12:00:00 GMT"
	res := precond.Resource{Exists: true, ETag: `"v"`, LastModified: t0}
	gone := precond.Resource{LastModified: t0}
	now := func() time.Time { return t0.Add(time.Hour) }
	ev := func(q precond.Request) precond.Result { return precond.Evaluate(q, res, now) }

	check("If-Match suppresses If-Unmodified-Since", ev(precond.Request{
		Method: "GET", IfMatch: `"v"`, IfUnmodifiedSince: "Thu, 24 Sep 2026 11:00:00 GMT",
	}) == precond.Continue)
	check("If-Match *: exists passes, missing fails",
		ev(precond.Request{Method: "GET", IfMatch: "*"}) == precond.Continue &&
			precond.Evaluate(precond.Request{Method: "GET", IfMatch: "*"}, gone, now) == precond.Failed)
	check("If-None-Match *: exists 304, missing passes",
		ev(precond.Request{Method: "GET", IfNoneMatch: "*"}) == precond.NotModified &&
			precond.Evaluate(precond.Request{Method: "GET", IfNoneMatch: "*"}, gone, now) == precond.Continue)
	check("list any-match with folding", ev(precond.Request{
		Method: "GET", IfNoneMatch: "\"a\",\r\n \"v\", \"b\"",
	}) == precond.NotModified)
	get := ev(precond.Request{Method: "GET", IfNoneMatch: `"v"`})
	put := ev(precond.Request{Method: "PUT", IfNoneMatch: `"v"`})
	check("read vs write differ", get == precond.NotModified && put == precond.Failed && get != put)
	check("If-Modified-Since equal: unmodified",
		ev(precond.Request{Method: "GET", IfModifiedSince: d0}) == precond.NotModified)
	check("If-Unmodified-Since equal: passes",
		ev(precond.Request{Method: "GET", IfUnmodifiedSince: d0}) == precond.Continue)
	check("unparsable headers ignored",
		ev(precond.Request{Method: "GET", IfModifiedSince: "junk", IfUnmodifiedSince: "junk"}) == precond.Continue)
	calls := 0
	precond.Evaluate(precond.Request{Method: "GET"}, res, func() time.Time { calls++; return t0 })
	check("no headers: clock untouched", calls == 0)
	check("81-combo matrix matches priority model", matrix(ev))
	q := precond.Request{Method: "GET", IfNoneMatch: `"v"`, IfModifiedSince: d0}
	check("repeat evaluation is stable", ev(q) == ev(q))
	fmt.Printf("total: %d failed\n", failed)
	if failed > 0 {
		panic("demo failed")
	}
}

// matrix enumerates absent/match/mismatch for all four headers (GET).
func matrix(ev func(precond.Request) precond.Result) bool {
	im := []string{"", `"v"`, `"z"`}
	ius := []string{"", "Thu, 24 Sep 2026 12:00:00 GMT", "Thu, 24 Sep 2026 11:00:00 GMT"}
	ims := []string{"", "Thu, 24 Sep 2026 11:00:00 GMT", "Thu, 24 Sep 2026 12:00:00 GMT"}
	for a := 0; a < 3; a++ {
		for b := 0; b < 3; b++ {
			for c := 0; c < 3; c++ {
				for d := 0; d < 3; d++ {
					q := precond.Request{Method: "GET", IfMatch: im[a],
						IfUnmodifiedSince: ius[b], IfNoneMatch: im[c], IfModifiedSince: ims[d]}
					if ev(q) != model(a, b, c, d) {
						return false
					}
				}
			}
		}
	}
	return true
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
