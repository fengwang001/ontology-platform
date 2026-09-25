// Command demo runs a few sanity checks on the egcd/num packages.
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/egcd"
	"ontology/num"
)

func main() {
	pass, total := 0, 0
	report := func(ok bool, name string) {
		total++
		if ok {
			pass++
			fmt.Println("OK", name)
		} else {
			fmt.Println("FAIL", name)
		}
	}
	g, x, y, err := egcd.ExtendedGCD(240, 46)
	report(err == nil && g == 2 && 240*x+46*y == 2, "bezout 240,46")
	g0, _, _, _ := egcd.ExtendedGCD(0, 0)
	ga, _, _, _ := egcd.ExtendedGCD(-7, 0)
	report(g0 == 0 && ga == 7, "gcd zero edges")
	inv, err := num.ModInverse(3, 11)
	report(err == nil && (3*inv)%11 == 1, "modinverse 3 mod 11")
	_, err = num.ModInverse(-1, 7)
	report(errors.Is(err, num.ErrBadArg), "badarg sentinel")
	_, err = num.ModInverse(2, 4)
	report(errors.Is(err, num.ErrNoInverse), "noinverse sentinel")
	_, _, _, _ = egcd.ExtendedGCD(679891637638612258, 420196140727489673)
	report(float64(egcd.MaxDepth()) <= 2*math.Log2(1e18)+2, "depth bound")
	_, x1, y1, _ := egcd.ExtendedGCD(240, 46)
	report(x1 == x && y1 == y, "deterministic")
	verdict := "OK"
	if pass != total {
		verdict = "FAIL"
	}
	fmt.Printf("%s total %d/%d\n", verdict, pass, total)
}
