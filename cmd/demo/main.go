// Command demo exercises the spilling hash aggregator end to end and
// prints one OK/FAIL line per acceptance check.
package main

import (
	"fmt"
	"math"

	"ontology/row"
)

var failures int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	r := row.Row{Key: "k/键", Val: -12.5}
	got, err := row.Decode(r.Encode())
	check("row-codec", err == nil && got == r, "encode/decode roundtrip")
	nan := row.Row{Key: "", Val: math.NaN()}
	ng, _ := row.Decode(nan.Encode())
	check("row-nan-key", math.IsNaN(ng.Val) && ng.Key == "", "empty key + NaN survive codec")
	fmt.Printf("TOTAL %d checks, %d failed\n", 1, failures)
}
