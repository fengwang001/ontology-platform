package main

import (
	"errors"
	"fmt"

	"ontology/check"
	"ontology/egcd"
	"ontology/num"
)

func main() {
	pass, total := 0, 0
	report := func(ok bool, name string) {
		total++
		if ok {
			pass++
			fmt.Println("OK   " + name)
		} else {
			fmt.Println("FAIL " + name)
		}
	}

	g, x, y, err := egcd.ExtendedGCD(240, 46)
	report(err == nil && g == 2 && 240*x+46*y == 2, "egcd(240,46) bezout")

	g0, _, _, err := egcd.ExtendedGCD(0, 0)
	report(err == nil && g0 == 0, "egcd(0,0)=0")

	gn, xn, yn, err := egcd.ExtendedGCD(-12, 18)
	report(err == nil && gn == 6 && -12*xn+18*yn == 6, "egcd(-12,18) signs")

	const big = 1_000_000_000_000_000_000
	gb, xb, yb, err := egcd.ExtendedGCD(big, big-7)
	report(err == nil && gb == 1 && check.BezoutOK(big, big-7, xb, yb, gb), "egcd(1e18) big")

	inv, err := num.ModInverse(3, 11)
	report(err == nil && inv == 4 && (3*inv)%11 == 1, "ModInverse(3,11)=4")

	_, err = num.ModInverse(2, 4)
	report(errors.Is(err, num.ErrNoInverse), "ModInverse(2,4) ErrNoInverse")

	_, err = num.ModInverse(-1, 5)
	report(errors.Is(err, num.ErrBadArg), "ModInverse(-1,5) ErrBadArg")

	_, xa, ya, _ := egcd.ExtendedGCD(240, 46)
	report(xa == x && ya == y, "egcd deterministic")

	verdict := "OK"
	if pass != total {
		verdict = "FAIL"
	}
	fmt.Printf("%s %d/%d checks passed\n", verdict, pass, total)
}
