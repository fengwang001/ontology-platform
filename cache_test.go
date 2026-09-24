package ontology_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"ontology/age"
	"ontology/directive"
	"ontology/verdict"
)

func TestDirectiveParse(t *testing.T) {
	cases := []struct {
		name, text, get string
		want            int64
		ok              bool
	}{
		{"plain", "max-age=60", "max-age", 60, true},
		{"quoted", `max-age="60"`, "max-age", 60, true},
		{"lowercase name", "Max-Age=60", "max-age", 60, true},
		{"signed ignored", "max-age=-5", "max-age", 0, false},
		{"plus ignored", "max-age=+5", "max-age", 0, false},
		{"decimal ignored", "max-age=1.5", "max-age", 0, false},
		{"non numeric ignored", "max-age=abc", "max-age", 0, false},
		{"missing delta ignored", "max-age", "max-age", 0, false},
		{"overflow clamped", "max-age=99999999999999999999999", "max-age", math.MaxInt64, true},
		{"first wins", "max-age=60, max-age=9", "max-age", 60, true},
		{"bare max-stale", "max-stale", "max-stale", directive.MaxStaleAny, true},
		{"bare no-cache", "no-cache", "no-cache", 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := directive.Parse(c.text)
			got, ok := s.Get(c.get)
			if ok != c.ok || (ok && got != c.want) {
				t.Fatalf("got (%d,%v), want (%d,%v)", got, ok, c.want, c.ok)
			}
		})
	}
	s := directive.Parse("no-cache, no-cache=100")
	if !s.Bare("no-cache") {
		t.Fatalf("first no-cache should remain bare")
	}
}

func TestAge(t *testing.T) {
	base := time.Unix(0, 0)
	at := func(req, resp, date time.Duration, hdr int64) age.Times {
		return age.Times{
			TReq:   base.Add(req * time.Second),
			TResp:  base.Add(resp * time.Second),
			TDate:  base.Add(date * time.Second),
			AgeHdr: hdr,
		}
	}
	cases := []struct {
		name string
		t    age.Times
		now  time.Duration
		want int64
	}{
		{"apparent age", at(0, 10, 0, 0), 10, 20},
		{"age header wins", at(0, 0, 0, 100), 0, 100},
		{"round trip adds", at(0, 10, 10, 0), 10, 10},
		{"resident time", at(0, 0, 0, 0), 30, 30},
		{"negative apparent clamped", at(0, 10, 20, 0), 10, 10},
		{"negative resident clamped", at(10, 10, 10, 0), 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			now := base.Add(c.now * time.Second)
			got, err := age.CurrentAge(c.t, now)
			if err != nil || got != c.want {
				t.Fatalf("got %d err=%v, want %d", got, err, c.want)
			}
		})
	}

	fresh := []struct {
		name    string
		resp    directive.Set
		expires time.Duration
		hasExp  bool
		src     age.Source
		secs    int64
	}{
		{"s-maxage first", directive.Parse("s-maxage=600, max-age=60"), 0, false, age.SourceSMaxAge, 600},
		{"max-age next", directive.Parse("max-age=60"), 0, false, age.SourceMaxAge, 60},
		{"expires fallback", directive.Parse("public"), 30, true, age.SourceExpires, 30},
		{"negative expires zero", directive.Parse("public"), -30, true, age.SourceExpires, 0},
		{"none zero", directive.Parse("public"), 0, false, age.SourceNone, 0},
	}
	for _, c := range fresh {
		t.Run(c.name, func(t *testing.T) {
			var exp time.Time
			if c.hasExp {
				exp = base.Add(c.expires * time.Second)
			}
			f, err := age.FreshnessLifetime(c.resp, at(0, 0, 0, 0), exp)
			if err != nil || f.Source != c.src || f.Seconds != c.secs {
				t.Fatalf("got %+v err=%v, want %s/%d", f, err, c.src, c.secs)
			}
		})
	}
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func TestVerdict(t *testing.T) {
	base := time.Unix(0, 0)
	cases := []struct {
		name, resp, req string
		ageSec          int
		want            verdict.Outcome
		reason          verdict.Reason
	}{
		{"fresh hit", "max-age=600", "", 100, verdict.Hit, verdict.ReasonFresh},
		{"stale revalidate", "max-age=60", "", 100, verdict.Revalidate, verdict.ReasonStale},
		{"smaxage shared", "s-maxage=600,max-age=60", "", 100, verdict.Hit, verdict.ReasonFresh},
		{"no-store dominates", "no-store,max-age=600", "", 0, verdict.Unusable, verdict.ReasonNoStore},
		{"resp no-cache", "no-cache,max-age=600", "", 0, verdict.Revalidate, verdict.ReasonRespNoCache},
		{"req no-cache", "max-age=600", "no-cache", 0, verdict.Revalidate, verdict.ReasonReqNoCache},
		{"min-fresh served stale", "max-age=100", "min-fresh=10", 95, verdict.Revalidate, verdict.ReasonStale},
		{"min-fresh fresh", "max-age=100", "min-fresh=4", 95, verdict.Hit, verdict.ReasonFresh},
		{"bare max-stale hit", "max-age=60", "max-stale", 260, verdict.Hit, verdict.ReasonMaxStale},
		{"bounded max-stale hit", "max-age=60", "max-stale=200", 260, verdict.Hit, verdict.ReasonMaxStale},
		{"bounded max-stale miss", "max-age=60", "max-stale=100", 260, verdict.Revalidate, verdict.ReasonStale},
		{"must-revalidate overrides", "max-age=60,must-revalidate", "max-stale", 260, verdict.Revalidate, verdict.ReasonMustRevalidate},
		{"req max-age overrides hit", "max-age=600", "max-age=50", 100, verdict.Revalidate, verdict.ReasonReqMaxAge},
		{"only-if-cached stale", "max-age=60", "only-if-cached", 100, verdict.Unusable, verdict.ReasonOnlyIfCached},
		{"only-if-cached fresh hit", "max-age=600", "only-if-cached", 10, verdict.Hit, verdict.ReasonFresh},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := verdict.Input{
				Times: age.Times{TReq: base, TResp: base, TDate: base},
				Resp:  directive.Parse(c.resp),
				Req:   directive.Parse(c.req),
				Clock: fixedClock{base.Add(time.Duration(c.ageSec) * time.Second)},
			}
			r, err := verdict.Decide(in)
			if err != nil || r.Outcome != c.want || r.Reason != c.reason {
				t.Fatalf("got %s/%s err=%v, want %s/%s", r.Outcome, r.Reason, err, c.want, c.reason)
			}
		})
	}
}

func TestErrors(t *testing.T) {
	base := time.Unix(0, 0)
	// 时钟未注入。
	if _, err := verdict.Decide(verdict.Input{}); !errors.Is(err, verdict.ErrNoClock) {
		t.Fatalf("want ErrNoClock, got %v", err)
	}
	// 时刻不自洽：响应早于请求。
	bad := age.Times{TReq: base.Add(10), TResp: base, TDate: base}
	if _, err := age.CurrentAge(bad, base); !errors.Is(err, age.ErrIncoherentTimestamps) {
		t.Fatalf("want ErrIncoherentTimestamps, got %v", err)
	}
	// 指令文本为空且响应无 Date。
	in := verdict.Input{
		Times: age.Times{TReq: base, TResp: base},
		Resp:  directive.Parse("  "),
		Clock: fixedClock{base},
	}
	if _, err := verdict.Decide(in); !errors.Is(err, age.ErrEmptyAndNoDate) {
		t.Fatalf("want ErrEmptyAndNoDate, got %v", err)
	}
}
