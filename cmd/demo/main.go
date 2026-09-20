// Command demo checks each documented semantic of package byterange
// and prints one OK/FAIL line per item. It always exits with code 0.
package main

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/byterange"
)

func main() {
	eq := func(h string, size int64, want []byterange.Range) bool {
		got, err := byterange.Parse(h, size)
		return err == nil && reflect.DeepEqual(got, want)
	}
	isErr := func(h string, size int64, want error) bool {
		got, err := byterange.Parse(h, size)
		return errors.Is(err, want) && got == nil
	}

	ok1 := eq("bytes=0-499", 1000, []byterange.Range{{Start: 0, End: 499}}) &&
		eq("bytes=500-", 1000, []byterange.Range{{Start: 500, End: 999}}) &&
		eq("bytes=-500", 1000, []byterange.Range{{Start: 500, End: 999}})
	ok2 := eq("bytes=0-999999", 1000, []byterange.Range{{Start: 0, End: 999}}) &&
		eq("bytes=-999999", 1000, []byterange.Range{{Start: 0, End: 999}}) &&
		isErr("bytes=1000-", 1000, byterange.ErrUnsatisfiable)
	ok3 := eq("bytes=1000-, 0-9", 1000, []byterange.Range{{Start: 0, End: 9}}) &&
		isErr("bytes=-0", 1000, byterange.ErrUnsatisfiable) &&
		isErr("bytes=1000-", 1000, byterange.ErrUnsatisfiable)
	ok4 := isErr("bytes=0-0", 0, byterange.ErrUnsatisfiable) &&
		isErr("bytes=-1", 0, byterange.ErrUnsatisfiable)
	ok5 := eq("bytes=0-100, 101-200, 150-300", 1000, []byterange.Range{{Start: 0, End: 300}}) &&
		eq("bytes=500-600, 0-100", 1000, []byterange.Range{{Start: 0, End: 100}, {Start: 500, End: 600}})
	ok6 := isErr("items=0-1", 1000, byterange.ErrMalformed) &&
		isErr("bytes=500-100", 1000, byterange.ErrMalformed) &&
		isErr("bytes=-", 1000, byterange.ErrMalformed) &&
		isErr("bytes=", 1000, byterange.ErrMalformed)
	ok7 := isErr("bytes=0-9223372036854775808", 1000, byterange.ErrMalformed) &&
		isErr("bytes=-9223372036854775808", 1000, byterange.ErrMalformed) &&
		eq("bytes=0-0", 1000, []byterange.Range{{Start: 0, End: 0}})
	a, ea := byterange.Parse("bytes=500-600, 0-100, -50", 1000)
	b, eb := byterange.Parse("bytes=500-600, 0-100, -50", 1000)
	ok8 := ea == nil && eb == nil && reflect.DeepEqual(a, b)

	items := []struct {
		name string
		ok   bool
	}{
		{"three forms normalize to closed intervals", ok1},
		{"out-of-range clipped, not an error", ok2},
		{"skip unsatisfiable; all-fail and -0 => ErrUnsatisfiable", ok3},
		{"size 0 => ErrUnsatisfiable", ok4},
		{"overlap/adjacent merged, sorted, disjoint", ok5},
		{"syntax errors => ErrMalformed with nil slice", ok6},
		{"int64 overflow => ErrMalformed; bytes=0-0 allowed", ok7},
		{"pure and repeatable", ok8},
	}
	failures := 0
	for i, it := range items {
		verdict := "OK  "
		if !it.ok {
			verdict = "FAIL"
			failures++
		}
		fmt.Printf("%s %d. %s\n", verdict, i+1, it.name)
	}
	fmt.Printf("summary: %d/%d passed\n", len(items)-failures, len(items))
}
