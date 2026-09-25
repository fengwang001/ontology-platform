// Command demo 演示十进制字符串 → 精确有理数解析器。
// 不读参数、不联网；退出码 0 表示全部判定通过；输出不超过 10 行。
package main

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"

	"ontology/api"
)

var failed bool

func check(name, detail string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%-4s %s %s\n", map[bool]string{true: "OK", false: "FAIL"}[ok], name, detail)
}

func floatFrac(x float64) string { // float64 的精确既约有理值
	b := math.Float64bits(x)
	mant := new(big.Int).SetUint64((b & (1<<52 - 1)) | 1<<52)
	denExp := uint(52 - (int((b>>52)&0x7ff) - 1023))
	den := new(big.Int).Lsh(big.NewInt(1), denExp)
	g := new(big.Int).GCD(nil, nil, mant, den)
	return new(big.Rat).SetFrac(new(big.Int).Quo(mant, g), new(big.Int).Quo(den, g)).RatString()
}

func benchParse(p *api.Parser, m int) time.Duration {
	s := "0." + strings.Repeat("1", m)
	for i := 0; i < 3; i++ { // warmup
		p.Parse(s)
	}
	reps, start := 500, time.Now()
	for i := 0; i < reps; i++ {
		_, err := p.Parse(s)
		if !errors.Is(err, api.ErrOverflow) { // 全程走完才在第 m 位前检测溢出
			return -1
		}
	}
	return time.Since(start) / time.Duration(reps)
}

func main() {
	p := api.New()
	eight := []string{"3.14", "0.1", "-2.5", "0.(3)", "0.1(6)", "123", "0.(142857)", "-0.0"}
	want := []string{"157/50", "1/10", "-5/2", "1/3", "1/6", "123/1", "1/7", "0/1"}
	got := make([]string, len(eight))
	ok8 := true
	for i, s := range eight {
		f, err := p.Parse(s)
		if err != nil {
			ok8 = false
		}
		got[i] = f.String()
		ok8 = ok8 && got[i] == want[i]
	}
	check("eight cases (incl. 1/3,1/6 exact)", strings.Join(got, " "), ok8)

	ff := floatFrac(0.1)
	check("float64(0.1) is not 1/10", "= "+ff, ff == "3602879701896397/36028797018963968")

	_, e9 := p.Parse("0.9999999999999999999")
	check("19 nines overflows int64", e9.Error(), errors.Is(e9, api.ErrOverflow))

	z1, _ := p.Parse("-0.0")
	z2, _ := p.Parse("0.(0)")
	check("zero normalization", z1.String()+" "+z2.String(), z1.String() == "0/1" && z2.String() == "0/1")

	check("matches big.Rat naive reference + reduced", "(SelfCheck)", api.SelfCheck() == nil)

	cases := []struct {
		s string
		e error
	}{{"", api.ErrEmpty}, {"1a", api.ErrIllegalChar}, {"1..2", api.ErrSyntax}, {"0.9999999999999999999", api.ErrOverflow}}
	distinct, oks := true, true
	seen := map[error]bool{}
	for _, c := range cases {
		_, err := p.Parse(c.s)
		oks = oks && errors.Is(err, c.e)
		if seen[err] || seen[c.e] {
			distinct = false
		}
		seen[err] = true
	}
	check("four distinct sentinel errors", fmt.Sprint(oks), oks && distinct)

	after, _ := p.Parse("3.14")
	check("usable after rejects, no state left", after.String(), after.String() == "157/50")

	t1, t4 := benchParse(p, 1000), benchParse(p, 4000) // 4 倍位数
	r := float64(t4) / float64(t1)
	check("mul10 single-pass O(m)", fmt.Sprintf("ratio %.2f (want 1.5..8)", r), t1 > 0 && r >= 1.5 && r <= 8)

	batch := append(append([]string{}, eight...), "1.(285714)", "1.0", "-0.(9)")
	serial := make([]api.Frac, len(batch))
	for i, s := range batch {
		f, _ := p.Parse(s)
		serial[i] = *f
	}
	var wg sync.WaitGroup
	concOK := true
	var mu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, s := range batch {
				f, err := p.Parse(s)
				mu.Lock()
				concOK = concOK && err == nil && *f == serial[i]
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	check("16 goroutines == serial", "(no sleep)", concOK)

	if failed {
		os.Exit(1)
	}
}
