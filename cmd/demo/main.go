package main

import (
	"fmt"
	"os"
	"time"

	"ontology/age"
	"ontology/directive"
	"ontology/verdict"
)

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type demoCase struct {
	name    string
	resp    string
	req     string
	ageHdr  int64
	want    verdict.Decision
	wantSrc string
	wantAge int64
}

func main() {
	base := time.Unix(0, 0)
	clock := fixedClock{base.Add(100 * time.Second)} // now - tResp = 100s

	cases := []demoCase{
		{"1 max-age=600 年龄100 直接命中", "max-age=600", "", 0, verdict.Hit, "max-age", 100},
		{"2 max-age=60 年龄100 再验证", "max-age=60", "", 0, verdict.Revalidate, "max-age", 100},
		{"3 s-maxage=600 优先于 max-age=60", "s-maxage=600, max-age=60", "", 0, verdict.Hit, "s-maxage", 100},
		{"4 Age 头大于表观年龄取 Age（年龄70）", "max-age=600", "", 50, verdict.Hit, "max-age", 70},
		{"5 no-cache 年龄0 再验证", "no-cache", "", 0, verdict.Revalidate, "none", 0},
		{"6 max-stale 无delta 过期200 仍命中", "max-age=100", "max-stale", 0, verdict.Hit, "max-age", 300},
		{"7 加 must-revalidate 改判再验证", "max-age=100, must-revalidate", "max-stale", 0, verdict.Revalidate, "max-age", 300},
		{"8 only-if-cached + 本应再验证 不可使用", "max-age=60", "only-if-cached", 0, verdict.Unusable, "max-age", 100},
	}

	failed := 0
	for _, c := range cases {
		e := age.Entry{
			TReq:     base,
			TResp:    base,
			TDate:    base,
			Response: directive.Parse(c.resp),
		}
		now := base.Add(time.Duration(c.wantAge) * time.Second)
		clock.t = now
		if c.ageHdr > 0 {
			e.AgeHdr, e.HasAgeHdr = c.ageHdr, true
			e.TResp = base.Add(10 * time.Second) // 70 = Age50 + 往返10 + 驻留10
			clock.t = base.Add(20 * time.Second)
		}
		res, err := verdict.Decide(verdict.Input{Entry: e, Request: directive.Parse(c.req), Clock: clock})
		ok := err == nil && res.Decision == c.want && res.Age == c.wantAge &&
			(c.wantSrc == "" || res.Source == c.wantSrc)
		status := "ok"
		if !ok {
			status, failed = "FAIL", failed+1
		}
		fmt.Printf("[%s] %s => 判定=%v 年龄=%d 新鲜期来源=%s 理由=%v\n",
			status, c.name, res.Decision, res.Age, res.Source, res.Reason)
	}

	if failed > 0 {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
