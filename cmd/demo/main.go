// Command demo 演示 float64 最短往返文本编解码器的各项判定。
package main

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"

	"ontology/bits"
	"ontology/dec"
	"ontology/fmtf"
	"ontology/parse"
)

var failures int

// sigDigits 返回文本中的有效数字串（去掉符号、小数点、指数与前导零）。
func sigDigits(s string) string {
	s = strings.TrimPrefix(s, "-")
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, ".", "")
	s = strings.TrimLeft(s, "0")
	return strings.TrimRight(s, "0")
}

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failures++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func main() {
	p := bits.Decompose(0.1)
	check("bits 拆解 0.1", !p.Neg && p.Exp == 1019 && p.Mant == 0x999999999999a,
		fmt.Sprintf("符号=%v 阶码=%d 尾数=%x", p.Neg, p.Exp, p.Mant))

	dec.ResetChecks()
	d, err := dec.Shortest(0.1)
	check("dec 0.1 一位有效数字", err == nil && d.Digits == "1" && d.Exp == -1,
		fmt.Sprintf("digits=%s exp=%d", d.Digits, d.Exp))
	check("dec 0.1 往返检查次数", dec.Checks() <= 3,
		fmt.Sprintf("checks=%d（<=3）", dec.Checks()))

	mx := math.Float64frombits(0x5d78399cbed80a3a) // 浮点回代会误判十六位可往返
	md, err := dec.Shortest(mx)
	check("dec 浮点回代误判例", err == nil && len(md.Digits) == 17,
		fmt.Sprintf("digits=%s（17 位）", md.Digits))

	s01, _ := fmtf.Format(0.1)
	check("fmtf 0.1 文本", s01 == "0.1", fmt.Sprintf("%q", s01))

	s13, _ := fmtf.Format(1.0 / 3.0)
	check("fmtf 1/3 位数不超 17", len(sigDigits(s13)) <= 17, fmt.Sprintf("%q", s13))

	b0, _ := fmtf.Format(1e16)
	b1, _ := fmtf.Format(1.5e17)
	b2, _ := fmtf.Format(1e-4)
	b3, _ := fmtf.Format(1e-5)
	check("fmtf 指数边界形式", b0 == "10000000000000000" && b1 == "1.5e+17" &&
		b2 == "0.0001" && b3 == "1e-05",
		fmt.Sprintf("%q %q %q %q", b0, b1, b2, b3))

	pz, _ := fmtf.Format(0.0)
	nz, _ := fmtf.Format(math.Copysign(0, -1))
	check("fmtf ±0 文本不同", pz == "0" && nz == "-0", fmt.Sprintf("%q %q", pz, nz))

	_, errNaN := fmtf.Format(math.NaN())
	_, errInf := fmtf.Format(math.Inf(1))
	check("fmtf NaN/Inf 报错", errNaN != nil && errInf != nil, "均返回可判定错误")

	y13, err := parse.Parse(s13)
	check("parse 1/3 往返逐位相同", err == nil &&
		math.Float64bits(y13) == math.Float64bits(1.0/3.0), "math.Float64bits 相等")

	pzBack, _ := parse.Parse(pz)
	nzBack, _ := parse.Parse(nz)
	check("parse ±0 各自往返",
		math.Float64bits(pzBack) == 0 &&
			math.Float64bits(nzBack) == math.Float64bits(math.Copysign(0, -1)),
		"符号位保留")

	roundtripRandom()

	sub := true
	for _, b := range []uint64{1, 2, 3, 0xfffffffffffff, 0x8000000000000} {
		x := math.Float64frombits(b)
		s, _ := fmtf.Format(x)
		y, err := parse.Parse(s)
		if err != nil || math.Float64bits(y) != b {
			sub = false
		}
	}
	check("次正规数往返", sub, "最小阶码附近抽样")

	fmt.Printf("总计: %d 项失败\n", failures)
	if failures > 0 {
		os.Exit(1)
	}
}

// roundtripRandom 十万个随机 float64：往返零失败且位数不多于标准库。
func roundtripRandom() {
	r := rand.New(rand.NewSource(20260924))
	n, fails, longer := 0, 0, 0
	for n < 100000 {
		x := math.Float64frombits(r.Uint64())
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		n++
		s, err := fmtf.Format(x)
		if err != nil {
			fails++
			continue
		}
		y, err := parse.Parse(s)
		if err != nil || math.Float64bits(y) != math.Float64bits(x) {
			fails++
		}
		if len(sigDigits(s)) > len(sigDigits(strconv.FormatFloat(x, 'g', -1, 64))) {
			longer++
		}
	}
	check("十万随机值往返", fails == 0, fmt.Sprintf("失败 %d/100000", fails))
	check("位数不多于标准库", longer == 0, fmt.Sprintf("更长 %d/100000", longer))
}
