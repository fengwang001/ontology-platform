package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/check"
	"ontology/egcd"
	"ontology/num"
)

var passed, total int

func judge(name string, ok bool) {
	total++
	if ok {
		passed++
		fmt.Println("OK", name)
	} else {
		fmt.Println("FAIL", name)
	}
}

func main() {
	g, x, y, _ := egcd.ExtendedGCD(240, 46)
	judge("egcd(240,46) bezout", g == 2 && 240*x+46*y == 2)
	inv, err := num.ModInverse(3, 11)
	judge("modinverse(3,11)=4", err == nil && inv == 4 && 3*inv%11 == 1)
	_, err = num.ModInverse(2, 4)
	judge("modinverse(2,4) no inverse", errors.Is(err, num.ErrNoInverse))
	_, err = num.ModInverse(-1, 5)
	judge("modinverse(-1,5) bad arg", errors.Is(err, num.ErrBadArg))
	judge("verify bezout pairs", check.VerifyBezout(240, 46) && check.VerifyBezout(-12, 18) &&
		check.VerifyBezout(0, 0) && check.VerifyBezout(1000000007, 123456789))
	fibA, fibB := 679891637638612258, 420196140727489673
	depth := egcd.RecursionDepth(fibA, fibB)
	judge("depth within log bound", depth <= 2*int(math.Log2(float64(fibA)))+2)
	g1, x1, y1, _ := egcd.ExtendedGCD(240, 46)
	judge("deterministic", g1 == g && x1 == x && y1 == y)
	bigA, bigB := 1000000000000000000, 999999999999999999
	bg, bx, by, _ := egcd.ExtendedGCD(bigA, bigB)
	judge("big int identity", bg == 1 && bigA*bx+bigB*by == 1)
	fmt.Printf("OK total %d/%d\n", passed, total)
	if passed != total {
		panic("demo failed")
	}
}
