// Command demo 逐条判定 egcd/num 的核心语义，全部通过时退出码为 0。
package main

import (
	"errors"
	"fmt"
	"os"

	"ontology/egcd"
	"ontology/num"
)

var passed, total int

func check(name string, ok bool) {
	total++
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	if ok {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	g1, x1, y1, _ := egcd.ExtendedGCD(240, 46)
	check("egcd(240,46) g=2 & bezout", g1 == 2 && 240*x1+46*y1 == 2)
	g2, _, _, _ := egcd.ExtendedGCD(0, 0)
	check("egcd(0,0) g=0", g2 == 0)
	g3, x3, y3, _ := egcd.ExtendedGCD(-12, 18)
	check("egcd(-12,18) g=6 & bezout", g3 == 6 && -12*x3+18*y3 == 6)
	inv, err := num.ModInverse(3, 11)
	check("ModInverse(3,11)=4", err == nil && inv == 4 && (3*inv)%11 == 1)
	_, err = num.ModInverse(2, 4)
	check("ModInverse(2,4) ErrNoInverse", errors.Is(err, num.ErrNoInverse))
	_, err = num.ModInverse(-1, 5)
	check("ModInverse(-1,5) ErrBadArg", errors.Is(err, num.ErrBadArg))
	big, err2 := num.ModInverse(1000000007, 1000000009)
	check("ModInverse big coprime pair", err2 == nil && (1000000007*big)%1000000009 == 1)
	_, _, _, err = egcd.ExtendedGCD(679891637638612258, 420196140727489673)
	check("depth bound holds (fib pair)", err == nil)
	fmt.Printf("OK total %d/%d\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
