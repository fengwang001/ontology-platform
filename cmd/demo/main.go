package main

import (
	"fmt"

	"ontology/internal/semver"
)

func report(ok bool, format string, args ...any) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
}

func main() {
	v, err := semver.Parse("1.2.3+build.42")
	report(err == nil && v.String() == "1.2.3+build.42", "Parse 并原样还原 build metadata: %s", v)

	a, _ := semver.Parse("1.0.0-alpha")
	b, _ := semver.Parse("1.0.0")
	report(semver.Compare(a, b) < 0, "1.0.0-alpha < 1.0.0")

	x, _ := semver.Parse("1.0.0-10")
	y, _ := semver.Parse("1.0.0-9")
	report(semver.Compare(x, y) > 0, "数字预发布段按数值比较: 10 > 9")

	p, _ := semver.Parse("1.0.0+x")
	q, _ := semver.Parse("1.0.0+y")
	report(semver.Compare(p, q) == 0, "build metadata 不参与比较")

	r1, _ := semver.ParseRange("^0.2.3")
	report(r1.Match(mustV("0.2.9")) && !r1.Match(mustV("0.3.0")), "^0.2.3 => >=0.2.3 <0.3.0")

	r2, _ := semver.ParseRange(">=1.0.0")
	report(!r2.Match(mustV("1.1.0-alpha")), ">=1.0.0 不匹配 1.1.0-alpha（预发布门控）")

	r3, _ := semver.ParseRange(">=1.1.0-0")
	report(r3.Match(mustV("1.1.0-alpha")), ">=1.1.0-0 匹配 1.1.0-alpha")

	lo, _ := semver.ParseRange(">=1.2.3")
	hi, _ := semver.ParseRange("<=1.2.3")
	pt, err := semver.Intersect(lo, hi)
	report(err == nil && pt.Match(mustV("1.2.3")) && !pt.Match(mustV("1.2.4")),
		">=1.2.3 与 <=1.2.3 求交为单点 1.2.3")

	_, err = semver.Intersect(mustR("<1.2.3"), mustR(">=1.2.3"))
	report(err != nil, "<1.2.3 与 >=1.2.3 冲突: %v", err)
}

func mustV(s string) semver.Version {
	v, err := semver.Parse(s)
	if err != nil {
		panic(err)
	}
	return v
}

func mustR(s string) semver.Range {
	r, err := semver.ParseRange(s)
	if err != nil {
		panic(err)
	}
	return r
}
