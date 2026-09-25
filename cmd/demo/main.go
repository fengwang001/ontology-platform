package main

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"ontology/api"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK", name)
}

func must(s string) *api.Int {
	v, err := api.FromString(s)
	if err != nil {
		panic(err)
	}
	return v
}

func main() {
	cases := [][4]string{{"17", "5", "3", "2"}, {"-17", "5", "-3", "-2"},
		{"17", "-5", "-3", "2"}, {"-17", "-5", "3", "-2"},
		{"1000000000", "7", "142857142", "6"}, {"-1", "1000000000", "0", "-1"},
		{"0", "-5", "0", "0"}, {"7", "-10", "0", "7"}}
	ok := true
	for _, c := range cases {
		q, r, err := must(c[0]).DivMod(must(c[1]))
		ok = ok && err == nil && q.String() == c[2] && r.String() == c[3]
	}
	check("divmod-8", ok)
	q, r, _ := must("9223372036854775807").DivMod(must("2"))
	check("2^63-1 div 2", q.String() == "4611686018427387903" && r.String() == "1")
	b1 := must("1000000000").Sub(must("1")).String() == "999999999"
	b2 := must("999999999").Mul(must("2")).String() == "1999999998"
	check("borrow/carry", b1 && b2)
	rt := true
	for _, s := range []string{"0", "-0", "000123", "-98765432109876543210987654321"} {
		v, err := api.FromString(s)
		w, err2 := api.FromString(v.String())
		rt = rt && err == nil && err2 == nil && w.String() == v.String()
	}
	check("roundtrip", rt)
	_, e1 := api.FromString("12a")
	_, e2 := api.FromString("1-2")
	_, _, e3 := must("1").DivMod(must("0"))
	check("errors-3", errors.Is(e1, api.ErrInvalidDigit) && errors.Is(e2, api.ErrBadSign) &&
		errors.Is(e3, api.ErrDivByZero) && e1 != e2 && e2 != e3 && e1 != e3)
	v := must("42")
	before := v.String()
	_, _ = api.FromString("-")
	_, _, _ = v.DivMod(must("0"))
	check("state-intact", v.String() == before && v.Add(must("8")).String() == "50")
	big := must("1")
	for i := 0; i < 20000; i++ {
		big = big.Mul(must("10"))
	}
	one := must("7")
	t1 := timeMul(big, one, 50)
	t2 := timeMul(big.Mul(must("100")), one, 50)
	check("mul-linear", t2 < 3*t1+time.Millisecond)
	shared := must("12345678901234567890")
	var wg sync.WaitGroup
	same := true
	start := make(chan struct{})
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if shared.String() != "12345678901234567890" || shared.Sign() != 1 || api.SelfCheck() != nil {
				same = false
			}
		}()
	}
	close(start)
	wg.Wait()
	check("concurrent-read", same)
	check("selfcheck", api.SelfCheck() == nil)
	if failed {
		os.Exit(1)
	}
}

func timeMul(a, b *api.Int, k int) time.Duration {
	t := time.Now()
	for i := 0; i < k; i++ {
		a.Mul(b)
	}
	return time.Since(t)
}
