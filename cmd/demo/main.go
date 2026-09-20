// demo 逐项验证 byterange 包的语义约定并打印 OK/FAIL。
package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/byterange"
)

var checks = []struct {
	name string
	ok   func() bool
}{
	{"1 three forms normalize", threeForms},
	{"2 clamp out-of-range", clamp},
	{"3 unsatisfiable vs skip", unsatisfiable},
	{"4 zero size", zeroSize},
	{"5 merge overlap/adjacent", merge},
	{"6 malformed syntax", malformed},
	{"7 int64 overflow", overflow},
	{"8 deterministic, no mutation", deterministic},
}

func main() {
	failed := 0
	for _, c := range checks {
		verdict := "OK"
		if !c.ok() {
			verdict = "FAIL"
			failed++
		}
		fmt.Printf("%-4s %s\n", verdict, c.name)
	}
	fmt.Printf("summary: %d/%d checks passed\n", len(checks)-failed, len(checks))
}

func parse(h string, size int64) ([]byterange.Range, error) {
	return byterange.Parse(h, size)
}

func eq(got []byterange.Range, err error, want []byterange.Range) bool {
	return err == nil && reflect.DeepEqual(got, want)
}

func threeForms() bool {
	a, e1 := parse("bytes=0-499", 1000)
	b, e2 := parse("bytes=500-", 1000)
	c, e3 := parse("bytes=-500", 1000)
	return eq(a, e1, []byterange.Range{{Start: 0, End: 499}}) &&
		eq(b, e2, []byterange.Range{{Start: 500, End: 999}}) &&
		eq(c, e3, []byterange.Range{{Start: 500, End: 999}})
}

func clamp() bool {
	a, e1 := parse("bytes=0-999999", 1000)
	b, e2 := parse("bytes=-999999", 1000)
	return eq(a, e1, []byterange.Range{{Start: 0, End: 999}}) &&
		eq(b, e2, []byterange.Range{{Start: 0, End: 999}})
}

func unsatisfiable() bool {
	got, err := parse("bytes=1000-,0-9", 1000)
	if !eq(got, err, []byterange.Range{{Start: 0, End: 9}}) {
		return false
	}
	_, err = parse("bytes=1000-", 1000)
	if !errors.Is(err, byterange.ErrUnsatisfiable) {
		return false
	}
	_, err = parse("bytes=-0", 1000)
	return errors.Is(err, byterange.ErrUnsatisfiable)
}

func zeroSize() bool {
	got, err := parse("bytes=0-", 0)
	return got == nil && errors.Is(err, byterange.ErrUnsatisfiable)
}

func merge() bool {
	got, err := parse("bytes=100-199,0-99,200-299,500-", 1000)
	return eq(got, err, []byterange.Range{{Start: 0, End: 299}, {Start: 500, End: 999}})
}

func malformed() bool {
	for _, h := range []string{"items=0-1", "bytes0-1", "bytes=500-100", "bytes=a-b", "bytes=-", "bytes="} {
		got, err := parse(h, 1000)
		if got != nil || !errors.Is(err, byterange.ErrMalformed) {
			return false
		}
	}
	_, err := parse("bytes=1000-", 1000)
	return !errors.Is(err, byterange.ErrMalformed)
}

func overflow() bool {
	_, err := parse("bytes=9223372036854775808-", 1000)
	if !errors.Is(err, byterange.ErrMalformed) {
		return false
	}
	got, err := parse("bytes=0-0", 1000)
	return eq(got, err, []byterange.Range{{Start: 0, End: 0}})
}

func deterministic() bool {
	h := "bytes=100-199,0-99,-50"
	a, e1 := parse(h, 1000)
	b, e2 := parse(h, 1000)
	return e1 == nil && e2 == nil && reflect.DeepEqual(a, b) &&
		h == "bytes=100-199,0-99,-50"
}
