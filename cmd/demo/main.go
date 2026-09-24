package main

import (
	"fmt"
	"math"

	"ontology/change"
)

var fails int

func check(name string, ok bool) {
	if ok {
		fmt.Println("OK  " + name)
		return
	}
	fails++
	fmt.Println("FAIL " + name)
}

func main() {
	// change: missing group / NaN rejected, valid change round-trips.
	missing := change.Change{Version: 1, Op: change.Insert, NewGroupOK: false, NewValue: 1}
	nan := change.Change{Version: 1, Op: change.Insert, NewGroupOK: true, NewGroup: "g", NewValue: math.NaN()}
	good := change.Change{Version: 1, Op: change.Insert, NewGroupOK: true, NewGroup: "", NewValue: -0.0}
	enc, encErr := good.Encode()
	dec, decErr := change.Decode(enc)
	check("change 拒绝缺失分组/NaN, 空串分组与负零可往返",
		encErr == nil && decErr == nil && dec == good &&
			missing.Valid() == change.ErrMissingGroup && nan.Valid() == change.ErrNaN)

	if fails == 0 {
		fmt.Println("TOTAL: ALL OK")
	} else {
		fmt.Printf("TOTAL: %d FAIL\n", fails)
	}
}
