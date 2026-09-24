package main

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"

	"ontology/bits"
	"ontology/dec"
	"ontology/fmtf"
	"ontology/parse"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func shortestOf(f float64) (string, int) {
	m, e2 := bits.Split(f).IntValue()
	return dec.Shortest(m, e2)
}

func enc(f float64) string {
	s, err := fmtf.Encode(f)
	if err != nil {
		return "ERR:" + err.Error()
	}
	return s
}

func roundTrips(f float64) bool {
	s, err := fmtf.Encode(f)
	if err != nil {
		return false
	}
	g, err := parse.Float(s)
	return err == nil && math.Float64bits(g) == math.Float64bits(f)
}

func sigCount(s string) int {
	if i := strings.IndexByte(s, 'e'); i >= 0 {
		s = s[:i]
	}
	s = strings.NewReplacer("-", "", ".", "").Replace(s)
	return len(strings.TrimRight(strings.TrimLeft(s, "0"), "0"))
}

func main() {
	pz := bits.Split(0.0)
	nz := bits.Split(math.Copysign(0, -1))
	m, e2 := bits.Split(math.SmallestNonzeroFloat64).IntValue()
	check("bits: signed zeros differ, subnormal splits", pz.Sign != nz.Sign && m == 1 && e2 == -1074,
		"+0/-0 sign bits distinct, min subnormal = 1*2^-1074")

	dec.ResetChecks()
	d1, e1 := shortestOf(0.1)
	check("dec: 0.1 shortest is 1 digit, checks<=3", d1 == "1" && e1 == -1 && dec.Checks() <= 3,
		fmt.Sprintf("digits=%q exp=%d checks=%d", d1, e1, dec.Checks()))

	d3, _ := shortestOf(1.0 / 3.0)
	check("dec: 1/3 <=17 digits and round-trips", len(d3) <= 17 && roundTrips(1.0/3.0),
		fmt.Sprintf("digits=%q (%d) bits-identical", d3, len(d3)))

	dt, _ := shortestOf(math.Float64frombits(0x3c04951aa42655d9))
	check("dec: float-backsubstitution trap needs 17", len(dt) == 17, fmt.Sprintf("digits=%q", dt))

	check("fmtf: 0.1 encodes as 0.1", enc(0.1) == "0.1", enc(0.1))

	boundary := enc(1e20) == "100000000000000000000" && enc(5e20) == "500000000000000000000" &&
		enc(1e21) == "1e+21" && enc(2.5e21) == "2.5e+21" &&
		enc(1e-4) == "0.0001" && enc(2e-4) == "0.0002" &&
		enc(1e-5) == "1e-5" && enc(3e-5) == "3e-5"
	check("fmtf: fixed/scientific boundary forms", boundary, "fixed iff -4<=E<21")

	_, errN := fmtf.Encode(math.NaN())
	_, errI := fmtf.Encode(math.Inf(-1))
	check("fmtf: NaN/Inf return sentinel errors", errors.Is(errN, fmtf.ErrNaN) && errors.Is(errI, fmtf.ErrInf),
		"ErrNaN/ErrInf via errors.Is")

	sPos, sNeg := enc(0.0), enc(math.Copysign(0, -1))
	check("parse: +0/-0 texts differ, both round-trip", sPos != sNeg && roundTrips(0.0) && roundTrips(math.Copysign(0, -1)),
		fmt.Sprintf("%q vs %q", sPos, sNeg))

	sub := true
	for _, b := range []uint64{1, 2, 3, 1 << 20, 1<<51 + 1, 1<<52 - 1} {
		sub = sub && roundTrips(math.Float64frombits(b))
	}
	check("parse: subnormal samples round-trip", sub, "bits 1,2,3,2^20,2^51+1,2^52-1")

	rng := rand.New(rand.NewSource(42))
	bad, longer, n := 0, 0, 0
	for i := 0; i < 100000; i++ {
		f := math.Float64frombits(rng.Uint64())
		if math.IsNaN(f) || math.IsInf(f, 0) {
			continue
		}
		n++
		if !roundTrips(f) {
			bad++
		}
		if f != 0 && sigCount(enc(f)) > sigCount(strconv.FormatFloat(f, 'g', -1, 64)) {
			longer++
		}
	}
	check("parse: 100k random round-trip zero failures", bad == 0, fmt.Sprintf("n=%d failures=%d", n, bad))
	check("parse: digits never longer than strconv", longer == 0, fmt.Sprintf("longer=%d of %d", longer, n))

	if failed > 0 {
		fmt.Printf("TOTAL FAIL (%d)\n", failed)
	} else {
		fmt.Println("TOTAL OK")
	}
}
