package gcra_test

import (
	"testing"

	"ontology/gcra"
)

func TestJudgeSpecFlow(t *testing.T) {
	// free T=1000 B=2, cost 1, no prior TAT at now=0.
	d := gcra.Judge(1000, 2, 1, 0, -1)
	want(t, d, true, 1000, 2, 1, 1, 0)
	d = gcra.Judge(1000, 2, 1, 0, d.TAT)
	want(t, d, true, 2000, 2, 0, 2, 0)
	d = gcra.Judge(1000, 2, 1, 0, d.TAT)
	want(t, d, false, 2000, 2, 0, 2, 1) // TAT unchanged at 2000
}

func TestJudgeBoundaries(t *testing.T) {
	// T=100 B=5 cost=1; prior TAT=500, now=100 => new=600, new-now=500 == burst.
	d := gcra.Judge(100, 5, 1, 100, 500)
	want(t, d, true, 600, 5, 0, 1, 0)
	// one ms earlier: new-now=501 > 500 -> limited, RetryAfter=ceil(1/1000)=1.
	d = gcra.Judge(100, 5, 1, 99, 500)
	want(t, d, false, 500, 5, 0, 1, 1)

	// cost == B: new-now = B*T exactly -> allowed.
	d = gcra.Judge(100, 3, 3, 0, -1)
	want(t, d, true, 300, 3, 0, 1, 0)
	// cost == B+1: never-allowed verdict, TAT untouched.
	d = gcra.Judge(100, 3, 4, 5000, -1)
	if d.Allowed || d.TAT != -1 {
		t.Fatalf("cost>B: got %+v", d)
	}

	// Exact-second ceilings: 2000ms -> 2.
	d = gcra.Judge(1000, 5, 1, 0, 1000) // a=1000,new=2000,new-now=2000
	want(t, d, true, 2000, 5, 3, 2, 0)
}

func want(t *testing.T, d gcra.Decision, allowed bool, tat, limit, remaining, reset, retryAfter int64) {
	t.Helper()
	if d.Allowed != allowed || d.TAT != tat || d.Headers.Limit != limit ||
		d.Headers.Remaining != remaining || d.Headers.Reset != reset ||
		d.Headers.RetryAfter != retryAfter {
		t.Fatalf("decision = %+v, want allowed=%v tat=%d limit=%d remaining=%d reset=%d retryAfter=%d",
			d, allowed, tat, limit, remaining, reset, retryAfter)
	}
}
