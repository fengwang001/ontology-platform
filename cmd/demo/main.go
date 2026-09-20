// demo 逐条检验 headers 包的 8 项语义并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/headers"
)

var failures int

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%-28s %s\n", name, status)
}

func main() {
	s, _ := headers.Parse("Host:   a  b \r\n", nil)
	v, _ := s.Get("Host")
	check("1 line format/trim", v == "a  b")
	_, err := headers.Parse("Host : v\r\n", nil)
	check("1 space-before-colon err", errors.Is(err, headers.ErrMalformed))

	s, _ = headers.Parse("content-type: x\r\nX-REQUEST-ID: y\r\n", nil)
	_, ok1 := s.Get("CONTENT-TYPE")
	check("2 case-insensitive+canon", ok1 && reflect.DeepEqual(s.Names(), []string{"Content-Type", "X-Request-Id"}))

	s, _ = headers.Parse("X-F: hi  \r\n \t there\r\n", nil)
	v, _ = s.Get("X-F")
	_, err = headers.Parse(" orphan\r\n", nil)
	check("3 obs-fold", v == "hi there" && errors.Is(err, headers.ErrMalformed))

	s, _ = headers.Parse("A: 1\r\nA: x,y\r\nA: 2\r\n", nil)
	v, _ = s.Get("A")
	check("4 multi-value order", v == "1, x,y, 2" && reflect.DeepEqual(s.Values("A"), []string{"1", "x,y", "2"}))

	s, err = headers.Parse("H: a\r\nH: a\r\n", []string{"h"})
	_, err2 := headers.Parse("H: a\r\nH: b\r\n", []string{"h"})
	check("5 single-value dedupe", err == nil && len(s.Values("H")) == 1 && errors.Is(err2, headers.ErrSingleValue))

	s, _ = headers.Parse("X-Empty:\r\n", nil)
	v, ok1 = s.Get("X-Empty")
	_, ok2 := s.Get("X-Missing")
	check("6 empty value kept", ok1 && v == "" && !ok2 && len(s.Values("X-Empty")) == 1)

	bad := []string{"NoColon\r\n", ": v\r\n", "B ad: v\r\n", "B\x01d: v\r\n", " f\r\n", "H: v\n", "H: v"}
	allMalformed, allNil := true, true
	for _, raw := range bad {
		s, err := headers.Parse(raw, nil)
		allMalformed = allMalformed && errors.Is(err, headers.ErrMalformed)
		allNil = allNil && s == nil
	}
	check("7 malformed -> nil set", allMalformed && allNil)

	raw := "H: a\r\nM: 1\r\nM: 2\r\n"
	s1, _ := headers.Parse(raw, nil)
	s2, _ := headers.Parse(raw, nil)
	same := reflect.DeepEqual(s1.Names(), s2.Names()) && reflect.DeepEqual(s1.Values("M"), s2.Values("M"))
	g1, _ := s1.Get("M")
	g2, _ := s2.Get("M")
	check("8 repeatable parse", same && g1 == g2)

	fmt.Printf("total: %d check(s), %d failure(s)\n", 10, failures)
}
