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

func main() {
	failed := false
	check := func(label string, ok bool) {
		status := "ok"
		if !ok {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("[%s] %s\n", status, label)
	}

	base := time.Unix(1_000_000, 0)
	// tReq/tResp/tDate 同一时刻，年龄完全由 now 相对 tResp 的秒数决定。
	decide := func(respHdr, reqHdr string, ageSec int, ageHdr int64) verdict.Result {
		in := verdict.Input{
			Times: age.Times{TReq: base, TResp: base, TDate: base, AgeHdr: ageHdr},
			Resp:  directive.Parse(respHdr),
			Req:   directive.Parse(reqHdr),
			Clock: fixedClock{base.Add(time.Duration(ageSec) * time.Second)},
		}
		r, err := verdict.Decide(in)
		if err != nil {
			fmt.Println("unexpected error:", err)
			failed = true
		}
		return r
	}

	cases := []struct {
		name, resp, req string
		ageSec          int
		want            verdict.Outcome
	}{
		{"1 max-age=600 age100 => hit", "max-age=600", "", 100, verdict.Hit},
		{"2 max-age=60 age100 => revalidate", "max-age=60", "", 100, verdict.Revalidate},
		{"3 s-maxage wins on shared cache", "s-maxage=600, max-age=60", "", 100, verdict.Hit},
		{"5 no-cache age0 => revalidate", "no-cache", "", 0, verdict.Revalidate},
		{"6 bare max-stale, stale 200 => hit", "max-age=60", "max-stale", 260, verdict.Hit},
		{"7 +must-revalidate => revalidate", "max-age=60, must-revalidate", "max-stale", 260, verdict.Revalidate},
		{"8 only-if-cached + stale => unusable", "max-age=60", "only-if-cached", 100, verdict.Unusable},
	}
	for _, c := range cases {
		r := decide(c.resp, c.req, c.ageSec, 0)
		check(c.name+" => "+string(r.Outcome), r.Outcome == c.want)
	}

	// 第 3 条打印新鲜期来源；第 4 条打印被 Age 头抬升后的年龄。
	r3 := decide("s-maxage=600, max-age=60", "", 100, 0)
	check(fmt.Sprintf("3 freshness source=%s seconds=%d", r3.Freshness.Source, r3.Freshness.Seconds),
		r3.Freshness.Source == age.SourceSMaxAge && r3.Freshness.Seconds == 600)
	r4 := decide("max-age=600", "", 0, 100)
	check(fmt.Sprintf("4 Age header lifts computed age=%d => %s", r4.Age, r4.Outcome),
		r4.Age == 100 && r4.Outcome == verdict.Hit)

	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
