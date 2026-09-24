package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/change"
)

type report struct{ fails int }

func (r *report) check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
	} else {
		r.fails++
		fmt.Println("FAIL " + name)
	}
}

func main() {
	r := &report{}

	// change: round-trip codec, NaN rejection, signed-zero equality.
	c := change.Change{Version: 1, ID: 7, Op: change.Update, KeyPresent: true,
		Key: "g", Val: -0.0, OldPresent: true, OldKey: "h", OldVal: 2.5}
	body, err := change.Marshal(c)
	got, err2 := change.Unmarshal(body)
	r.check("change codec round-trip +0/-0 equal",
		err == nil && err2 == nil && got.ID == 7 &&
			math.Float64bits(got.Val) == math.Float64bits(0.0))
	nan := c
	nan.Val = math.NaN()
	_, err = change.Marshal(nan)
	r.check("change rejects NaN and missing key",
		errors.Is(err, change.ErrNaN) &&
			errors.Is(func() error { _, e := change.Marshal(change.Change{Op: change.Insert}); return e }(),
				change.ErrMissingKey))

	if r.fails == 0 {
		fmt.Println("TOTAL: all checks passed")
	} else {
		fmt.Printf("TOTAL: %d check(s) failed\n", r.fails)
	}
}
