package main

import (
	"fmt"
	"os"

	"ontology/age"
	"ontology/directive"
)

type check struct {
	name string
	got  any
	want any
}

func main() {
	ok := true

	s, err := directive.Parse(`Max-Age="60", NO-STORE, max-age=999, junk=1.5, max-stale`)
	if err != nil {
		panic(err)
	}
	checks := []check{
		{"first max-age wins / quoted == 60", must(s.Delta("max-age")), int64(60)},
		{"name lower-cased no-store", s.Present("no-store"), true},
		{"invalid item junk ignored", s.Present("junk"), false},
		{"bare max-stale present", s.Present("max-stale"), true},
		{"bare max-stale has no argument", hasArg(s.Delta("max-stale")), false},
	}
	ok = run("directive", checks) && ok

	// Fixed timeline: request at 0, response at 10, date at 0, now at 100.
	base := age.Params{TReq: 0, TResp: 10, TDate: 0}
	clock := age.ClockFunc(func() int64 { return 100 })
	cc3, _ := directive.Parse("s-maxage=600, max-age=60")
	fresh3, src3 := age.Freshness(cc3, 0, 0, false)
	p4 := age.Params{TReq: 0, TResp: 10, TDate: 0, HasAge: true, AgeHdr: 50}
	age4, err := age.CurrentAge(clock, p4)
	if err != nil {
		panic(err)
	}
	agePlain, err := age.CurrentAge(clock, base)
	if err != nil {
		panic(err)
	}
	ageChecks := []check{
		{"#3 s-maxage beats max-age, source", src3, age.SourceSMaxAge},
		{"#3 freshness value 600", fresh3, int64(600)},
		{"#4 age takes Age header (50+10+90)", age4, int64(150)},
		{"plain age without Age header (10+90)", agePlain, int64(100)},
	}
	ok = run("age", ageChecks) && ok

	if ok {
		fmt.Println("ALL OK")
		return
	}
	os.Exit(1)
}

func must(v int64, ok bool) int64 {
	if !ok {
		panic("expected argument present")
	}
	return v
}

func hasArg(_ int64, ok bool) bool { return ok }

func run(scope string, checks []check) bool {
	all := true
	for _, c := range checks {
		if fmt.Sprint(c.got) != fmt.Sprint(c.want) {
			fmt.Printf("[FAIL] %s: %s got=%v want=%v\n", scope, c.name, c.got, c.want)
			all = false
		}
	}
	return all
}
