package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/etag"
	"ontology/httpdate"
)

func main() {
	pass, total := 0, 0
	check := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}
	mustTag := func(s string) etag.Tag {
		t, err := etag.Parse(s)
		if err != nil {
			panic(err)
		}
		return t
	}
	strong := mustTag(`"x"`)
	weak := mustTag(`W/"x"`)
	check("strong vs strong equal", etag.StrongEqual(strong, strong))
	check("weak   vs strong not strong-equal", !etag.StrongEqual(weak, strong))
	check("weak   vs weak   not strong-equal", !etag.StrongEqual(weak, weak))
	check("weak   vs strong weak-equal", etag.WeakEqual(weak, strong))
	tags, wildcard, err := etag.ParseList("*\t")
	check("wildcard list parsed", err == nil && wildcard && tags == nil)
	tags, wildcard, err = etag.ParseList("\"a\", \"b\"")
	check("list any-match", err == nil && !wildcard && etag.WeakAny(mustTag(`"b"`), tags))
	t1, derr := httpdate.Parse("Mon, 02 Jan 2006 15:04:05 GMT")
	_, eSpace := httpdate.Parse(" Mon, 02 Jan 2006 15:04:05 GMT")
	_, eSuffix := httpdate.Parse("Mon, 02 Jan 2006 15:04:05 UTC")
	_, eMonth := httpdate.Parse("Mon, 02 Xxx 2006 15:04:05 GMT")
	_, eDate := httpdate.Parse("Fri, 30 Feb 2006 15:04:05 GMT")
	distinct := errors.Is(eSpace, httpdate.ErrWhitespace) &&
		errors.Is(eSuffix, httpdate.ErrSuffix) && errors.Is(eMonth, httpdate.ErrMonth) &&
		errors.Is(eDate, httpdate.ErrDate)
	check("httpdate parse + four distinct errors", derr == nil && distinct)
	t2, _ := httpdate.Parse("Mon, 02 Jan 2006 15:04:05 GMT")
	check("equal-instant critical point parses equal", t1.Equal(t2) && !t1.Before(t2))
	check("one-second-later is strictly after", t1.Add(time.Second).After(t2))
	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		panic("demo failed")
	}
}
