package verdict_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"ontology/age"
	"ontology/directive"
	"ontology/verdict"
)

type clk struct{ t time.Time }

func (c clk) Now() time.Time { return c.t }

func TestDirectiveParse(t *testing.T) {
	cases := []struct {
		name, text, key          string
		wantDelta                int64
		present, delta, infinite bool
	}{
		{`引号 max-age="60"`, `max-age="60"`, "max-age", 60, true, true, false},
		{"普通", "max-age=60", "max-age", 60, true, true, false},
		{"负数忽略", "max-age=-5", "max-age", 0, false, false, false},
		{"非数字忽略", "max-age=abc", "max-age", 0, false, false, false},
		{"溢出取极大", "max-age=999999999999999999999", "max-age", math.MaxInt64, true, true, false},
		{"无delta max-stale", "max-stale", "max-stale", 0, true, false, true},
		{"缺delta忽略", "max-age", "max-age", 0, false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, ok := directive.Parse(c.text).Get(c.key)
			if ok != c.present || d.HasDelta != c.delta || d.Infinite != c.infinite {
				t.Fatalf("flags: got (present=%v delta=%v inf=%v)", ok, d.HasDelta, d.Infinite)
			}
			if c.delta && d.Delta != c.wantDelta {
				t.Fatalf("delta: got %d want %d", d.Delta, c.wantDelta)
			}
		})
	}
	p := directive.Parse("MAX-AGE=10, max-age=20, max-age=abc, no-cache")
	if d, _ := p.Get("max-age"); d.Delta != 10 || !p.Has("no-cache") {
		t.Fatal("dup-first / sibling-survive rule violated")
	}
}

func TestAgeAndFreshness(t *testing.T) {
	base := time.Unix(0, 0)
	e := age.Entry{
		TReq: base, TResp: base.Add(10 * time.Second), TDate: base,
		AgeHdr: 50, HasAgeHdr: true, Response: directive.Parse(""),
	}
	if got, err := age.CurrentAge(e, base.Add(20*time.Second)); err != nil || got != 70 {
		t.Fatalf("age got %d err %v, want 70 nil", got, err) // 50 + 往返10 + 驻留10
	}
	if _, err := age.CurrentAge(age.Entry{TReq: base.Add(time.Second), TResp: base}, base.Add(time.Second)); !errors.Is(err, age.ErrInconsistentTime) {
		t.Fatalf("want ErrInconsistentTime, got %v", err)
	}
	if _, _, err := age.Freshness(age.Entry{Response: directive.Parse("")}); !errors.Is(err, age.ErrNoDateAndDirective) {
		t.Fatalf("want ErrNoDateAndDirective, got %v", err)
	}
	fresh := []struct {
		resp      string
		exp, want int64
		src       string
	}{
		{"s-maxage=600, max-age=60", 0, 600, "s-maxage"},
		{"max-age=60", 0, 60, "max-age"},
		{"", 30, 30, "expires"},
		{"", -10, 0, "expires"},
		{"public", 0, 0, "none"},
	}
	for _, f := range fresh {
		fe := age.Entry{TDate: base, Response: directive.Parse(f.resp)}
		if f.src == "expires" {
			fe.HasExpires, fe.Expires = true, base.Add(time.Duration(f.exp)*time.Second)
		}
		v, src, err := age.Freshness(fe)
		if err != nil || v != f.want || src != f.src {
			t.Fatalf("freshness %q: got (%d,%s,%v) want (%d,%s)", f.resp, v, src, err, f.want, f.src)
		}
	}
}

func TestVerdict(t *testing.T) {
	base := time.Unix(0, 0)
	decide := func(resp, req string, a int64) (verdict.Result, error) {
		e := age.Entry{TReq: base, TResp: base, TDate: base, Response: directive.Parse(resp)}
		return verdict.Decide(verdict.Input{
			Entry: e, Request: directive.Parse(req),
			Clock: clk{base.Add(time.Duration(a) * time.Second)},
		})
	}
	cases := []struct {
		resp, req string
		age       int64
		want      verdict.Decision
		reason    verdict.Reason
	}{
		{"no-store", "", 0, verdict.Unusable, verdict.ReasonStore},
		{"no-cache", "", 0, verdict.Revalidate, verdict.ReasonRespNoCache},
		{"max-age=600", "", 100, verdict.Hit, verdict.ReasonFresh},
		{"max-age=60", "", 100, verdict.Revalidate, verdict.ReasonStale},
		{"max-age=100", "max-stale", 300, verdict.Hit, verdict.ReasonMaxStale},
		{"max-age=100, must-revalidate", "max-stale", 300, verdict.Revalidate, verdict.ReasonMustRevalidate},
		{"max-age=600", "max-age=50", 100, verdict.Revalidate, verdict.ReasonReqMaxAge},
		{"max-age=60", "only-if-cached", 100, verdict.Unusable, verdict.ReasonOnlyIfCached},
	}
	for _, c := range cases {
		r, err := decide(c.resp, c.req, c.age)
		if err != nil || r.Decision != c.want || r.Reason != c.reason {
			t.Fatalf("%s|%s a=%d: got (%v,%v,%v) want (%v,%v)", c.resp, c.req, c.age, r.Decision, r.Reason, err, c.want, c.reason)
		}
	}
	if _, err := verdict.Decide(verdict.Input{Clock: nil}); !errors.Is(err, verdict.ErrClockNotInjected) {
		t.Fatalf("want ErrClockNotInjected, got %v", err)
	}
}
