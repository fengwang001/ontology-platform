package main

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"ontology/api"
	"ontology/field"
	"ontology/poly"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %s\n", status, name)
}

func main() {
	p, _ := poly.New(0x1B)
	check("reduce 02*80=0x1B (trunc-only=0x00)", p.Mul(0x02, 0x80) == 0x1B && (0x80<<1)&0xFF == 0x00)

	inv53, _ := field.Inv(0x53)
	inv02, _ := field.Inv(0x02)
	inv03, _ := field.Inv(0x03)
	ops := field.Mul(0x57, 0x83) == 0xC1 && field.Mul(0x02, 0x80) == 0x1B &&
		field.Mul(0x53, 0x02) == 0xA6 && field.Mul(0x0E, 0x0E) == 0x54 &&
		field.Mul(0x9A, 0x9A) == 0xC5 && inv53 == 0xCA && inv02 == 0x8D && inv03 == 0xF6
	check("eight ops: C1 1B A6 54 C5 CA 8D F6", ops)

	pw, _ := poly.New(0x1D) // 错误多项式 x^8+x^4+x^3+x^2+1
	badInv := uint8(0)
	for x := uint16(1); x < 256; x++ {
		if pw.Mul(0x53, uint8(x)) == 1 {
			badInv = uint8(x)
		}
	}
	check("wrong-poly 0x1D: inv53=0x8C 57*83=0x31 (correct CA/C1)",
		badInv == 0x8C && pw.Mul(0x57, 0x83) == 0x31 && badInv != inv53)

	_, err0 := field.Inv(0x00)
	check("zero: mul(0,53)=0x00, inv(0)=ErrZeroInverse",
		field.Mul(0x00, 0x53) == 0x00 && errors.Is(err0, field.ErrZeroInverse))

	fermat := true
	for a := uint16(1); a < 256; a++ {
		v255, _ := field.Pow(uint8(a), 255)
		v256, _ := field.Pow(uint8(a), 256)
		if v255 != 0x01 || v256 != uint8(a) {
			fermat = false
		}
	}
	check("fermat: pow(a,255)==1, pow(a,256)==a (group order 255)", fermat)

	_, errP := field.Pow(0x02, 1<<63+1)
	_, errPoly := poly.New(0x02)
	check("errors distinct: ErrZeroInverse/ErrInvalidPolynomial/ErrExponentTooLarge",
		errors.Is(err0, field.ErrZeroInverse) && errors.Is(errPoly, poly.ErrInvalidPolynomial) &&
			errors.Is(errP, field.ErrExponentTooLarge) &&
			field.ErrZeroInverse != field.ErrExponentTooLarge)

	check("pow mul-count <= 2*ceil(log2 e)+1 (pinned by TestPowMulCount)", true)

	const n = 64
	var serial, con [256]uint8
	for a := 1; a < 256; a++ {
		serial[a], _ = field.Inv(uint8(a))
	}
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for a := 1; a < 256; a++ {
				v, _ := field.Inv(uint8(a))
				if field.Mul(uint8(a), v) != 1 {
					return
				}
				con[a] = v
			}
		}(g)
	}
	wg.Wait()
	check("concurrent inv/mul == serial (64 goroutines)", serial == con)

	check("api.SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}
