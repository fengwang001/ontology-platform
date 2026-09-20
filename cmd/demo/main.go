package main

import (
	"errors"
	"fmt"
	"unsafe"

	"ontology/headers"
)

type check struct {
	name string
	ok   bool
}

func main() {
	var results []check

	s, err := headers.Parse("Host: example.com \r\nX-Inner: a  b\r\n", nil)
	host, _ := s.Get("Host")
	inner, _ := s.Get("X-Inner")
	_, badErr := headers.Parse("Host : x\r\n", nil)
	results = append(results, check{
		"1 line format & trim",
		err == nil && host == "example.com" && inner == "a  b" && errors.Is(badErr, headers.ErrMalformed),
	})

	s2, _ := headers.Parse("content-type: text/plain\r\nX-REQUEST-ID: 7\r\n", nil)
	ct, _ := s2.Get("CONTENT-TYPE")
	results = append(results, check{
		"2 case-insensitive, canonical names",
		ct == "text/plain" && fmt.Sprint(s2.Names()) == "[Content-Type X-Request-Id]",
	})

	s3, _ := headers.Parse("X-F: first  \r\n second\r\n\tthird\r\n", nil)
	fv, _ := s3.Get("X-F")
	_, foldErr := headers.Parse(" orphan\r\n", nil)
	results = append(results, check{
		"3 obs-fold continuation",
		fv == "first second third" && errors.Is(foldErr, headers.ErrMalformed),
	})

	s4, _ := headers.Parse("Set-Cookie: a=1\r\nSet-Cookie: b=2, x=3\r\n", nil)
	g4, _ := s4.Get("Set-Cookie")
	results = append(results, check{
		"4 multi-values ordered, inner comma kept",
		fmt.Sprint(s4.Values("Set-Cookie")) == "[a=1 b=2, x=3]" && g4 == "a=1, b=2, x=3",
	})

	s5, e5 := headers.Parse("CL: 5\r\ncl: 5\r\n", []string{"cl"})
	_, e5b := headers.Parse("CL: 5\r\nCL: 6\r\n", []string{"cl"})
	v5 := s5.Values("CL")
	results = append(results, check{
		"5 single-value dedupe vs conflict",
		e5 == nil && len(v5) == 1 && errors.Is(e5b, headers.ErrSingleValue),
	})

	s6, _ := headers.Parse("X-Empty:\r\n", nil)
	ev, present := s6.Get("X-Empty")
	_, absent := s6.Get("Absent")
	results = append(results, check{
		"6 empty value retained",
		present && ev == "" && !absent && len(s6.Names()) == 1,
	})

	_, e7 := headers.Parse("No-Colon line\r\n", nil)
	s7, _ := headers.Parse("Bad: x\r\n", nil)
	results = append(results, check{
		"7 syntax errors are ErrMalformed, nil set",
		errors.Is(e7, headers.ErrMalformed) && s7 != nil,
	})

	s8a, _ := headers.Parse("X-A: 1\r\nX-B: 2\r\n", nil)
	s8b, _ := headers.Parse("X-A: 1\r\nX-B: 2\r\n", nil)
	buf := []byte("X-A: 1\r\nX-B: 2\r\n")
	s8c, _ := headers.Parse(unsafe.String(&buf[0], len(buf)), nil)
	for i := range buf {
		buf[i] = '!'
	}
	results = append(results, check{
		"8 no input alias & repeatable",
		fmt.Sprint(s8a.Names()) == fmt.Sprint(s8b.Names()) &&
			fmt.Sprint(s8a.Values("X-A")) == fmt.Sprint(s8b.Values("X-A")) &&
			fmt.Sprint(s8c.Names()) == "[X-A X-B]",
	})

	for _, r := range results {
		status := "FAIL"
		if r.ok {
			status = "OK"
		}
		fmt.Printf("%s %s\n", status, r.name)
	}
}
