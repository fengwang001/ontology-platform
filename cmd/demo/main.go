package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/egcd"
	"ontology/num"
)

func main() {
	fails := 0
	report := func(name string, ok bool) {
		verdict := "OK"
		if !ok {
			verdict = "FAIL"
			fails++
		}
		fmt.Printf("%s %s\n", verdict, name)
	}
	g, x, y, err := egcd.ExtendedGCD(240, 46)
	report("egcd(240,46) bezout", err == nil && g == 2 && 240*x+46*y == 2)
	g0, _, _, _ := egcd.ExtendedGCD(0, 0)
	report("egcd(0,0)==0", g0 == 0)
	gn, xn, yn, _ := egcd.ExtendedGCD(-12, 18)
	report("egcd(-12,18) bezout", gn == 6 && -12*xn+18*yn == 6)
	inv, err := num.ModInverse(3, 11)
	report("inv(3,11)==4", err == nil && inv == 4 && 3*inv%11 == 1)
	_, err = num.ModInverse(2, 4)
	report("inv(2,4) ErrNoInverse", errors.Is(err, num.ErrNoInverse))
	_, err = num.ModInverse(-1, 5)
	report("inv(-1,5) ErrBadArg", errors.Is(err, num.ErrBadArg))
	report("depth bound fib(88),fib(87)",
		egcd.Depth(1100087778366101931, 679891637638612258) <= 124)
	if fails > 0 {
		fmt.Printf("FAIL total: %d failed\n", fails)
		os.Exit(1)
	}
	fmt.Println("OK total: 7/7 passed")
}
