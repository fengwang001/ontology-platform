package sampler

import (
	"errors"
	"testing"
)

func baseCfg() Config {
	// W=10 Sc=3 Nmax=2 L=100 P=5000 Wb=1000 Q=1 Td=50 Cmax=100
	return Config{W: 10, Sc: 3, Nmax: 2, L: 100, P: 5000, Wb: 1000, Q: 1, Td: 50, Cmax: 100}
}

func hash100(s string) uint32 {
	if s == "t2" {
		return 100
	}
	return 9000 // sampled out by default, unless a test overrides P
}

func mustIngest(t *testing.T, s *Sampler, now int64, tid, sid string, dur int64, err bool) []Decision {
	t.Helper()
	ds, e := s.Ingest(now, tid, sid, dur, err)
	if e != nil {
		t.Fatalf("Ingest(%s,%s) unexpected error: %v", tid, sid, e)
	}
	return ds
}

func TestSpecExample(t *testing.T) {
	cfg := baseCfg()
	cfg.H = hash100
	s, _ := New(cfg)
	mustIngest(t, s, 0, "t1", "a", 30, false)
	mustIngest(t, s, 4, "t1", "b", 120, false)
	mustIngest(t, s, 5, "t2", "c", 10, false)

	ds, _ := s.Tick(14)
	if len(ds) != 1 || !ds[0].Keep || ds[0].Reason != ReasonLatency || ds[0].Spans != 2 {
		t.Fatalf("tick14 = %+v", ds)
	}
	t.Logf("input=Tick14 output=%+v why=lastSeen4+W10=14,maxDur120>=L100,u=1", ds)

	ds, _ = s.Tick(15)
	if len(ds) != 1 || ds[0].Keep || ds[0].Reason != ReasonBudget {
		t.Fatalf("tick15 = %+v", ds)
	}
	t.Logf("input=Tick15 output=%+v why=h(t2)=100<5000 prob but u=1>=Q=1", ds)

	if ds := mustIngest(t, s, 20, "t1", "d", 5, false); len(ds) != 0 {
		t.Fatal("cache hit must produce no decision")
	}
	if st := s.Stats(); st.LateKept != 1 || st.LateDropped != 0 {
		t.Fatalf("stats=%+v", st)
	}
	t.Logf("input=late t1@20 output=LateKept why=14+50=64>20 cache live")

	mustIngest(t, s, 64, "t1", "e", 5, false) // expired -> brand new trace
	ds, _ = s.Tick(74)
	if len(ds) != 1 || ds[0].Spans != 1 {
		t.Fatalf("post-expiry decision=%+v", ds) // old 2 spans must not merge
	}
	t.Logf("input=late t1@64 Tick74 output=%+v why=cache expired at64, fresh trace", ds)
}

func TestSilenceExactlyW(t *testing.T) {
	s, _ := New(baseCfg())
	mustIngest(t, s, 0, "t", "a", 1, false)
	if ds, _ := s.Tick(9); len(ds) != 0 {
		t.Fatalf("lastSeen+W=10 > 9: %+v", ds)
	}
	ds, _ := s.Tick(10)
	if len(ds) != 1 || ds[0].At != 10 {
		t.Fatalf("boundary tick=%+v", ds)
	}
	t.Logf("input=Tick9,Tick10 output=none,one why=0+10<=10 inclusive")
}

func TestScImmediate(t *testing.T) {
	s, _ := New(baseCfg())
	if ds := mustIngest(t, s, 0, "t", "a", 200, false); len(ds) != 0 {
		t.Fatalf("first span: %+v", ds)
	}
	ds := mustIngest(t, s, 1, "t", "b", 1, false)
	if len(ds) != 0 {
		t.Fatalf("second span: %+v", ds)
	}
	ds = mustIngest(t, s, 2, "t", "c", 1, false)
	if len(ds) != 1 || !ds[0].Keep || ds[0].Reason != ReasonLatency || ds[0].Spans != 3 || ds[0].At != 2 {
		t.Fatalf("Sc trigger=%+v", ds)
	}
	t.Logf("input=3rd span@2 output=%+v why=spans==Sc decides at now", ds)
}

func TestEvictionOrderAndQuota(t *testing.T) {
	cfg := baseCfg()
	cfg.H = func(string) uint32 { return 1 } // all Prob candidates
	s, _ := New(cfg)
	mustIngest(t, s, 100, "x", "a", 1, false)
	mustIngest(t, s, 100, "y", "a", 1, false)
	ds := mustIngest(t, s, 101, "z", "a", 1, false)
	// "x" evicted first: Prob, u=0<Q=1 => kept and consumes quota.
	if len(ds) != 1 || ds[0].TraceID != "x" || !ds[0].Evicted || !ds[0].Keep ||
		ds[0].Reason != ReasonProb || ds[0].At != 101 {
		t.Fatalf("eviction decision=%+v", ds)
	}
	ds, _ = s.Tick(111)
	// y then z both silent; quota exhausted => Budget.
	if len(ds) != 2 || ds[0].TraceID != "y" || ds[0].Keep || ds[1].TraceID != "z" || ds[1].Reason != ReasonBudget {
		t.Fatalf("tick=%+v", ds)
	}
	t.Logf("input=(x,y)@100,z@101 output=x evicted kept; y,z Budget why=(100,x) min, u=1")
}

func TestWindowResetAndErrorOverflow(t *testing.T) {
	cfg := baseCfg()
	cfg.Wb = 10
	cfg.H = func(string) uint32 { return 1 }
	s, _ := New(cfg)
	mustIngest(t, s, 0, "e", "a", 1, true)
	ds, _ := s.Tick(10) // Error kept in window 1, u=1 (>Q=1)
	if !ds[0].Keep || ds[0].Reason != ReasonError {
		t.Fatalf("error decision=%+v", ds)
	}
	mustIngest(t, s, 10, "p", "a", 1, false)
	ds, _ = s.Tick(20) // still window 1... floor(20/10)=2 actually: new window
	if !ds[0].Keep || ds[0].Reason != ReasonProb {
		t.Fatalf("window flip should reset u: %+v", ds)
	}
	t.Logf("input=Error@win1,Prob@win2 output=both kept why=window flip resets u")

	// Same-window Error overflow blocks Prob.
	s2, _ := New(cfg)
	mustIngest(t, s2, 0, "e", "a", 1, true)
	mustIngest(t, s2, 1, "p", "a", 1, false)
	ds, _ = s2.Tick(11) // window floor(11/10)=1 for both
	if len(ds) != 2 || ds[0].Reason != ReasonError || ds[1].Reason != ReasonBudget {
		t.Fatalf("same window=%+v", ds)
	}
	t.Logf("input=Error then Prob same window output=Error,Budget why=u can exceed Q")
}

func TestValidationAndRejections(t *testing.T) {
	bad := []Config{
		{W: -1}, {W: 1e9 + 1}, {Td: -1}, {Sc: 0}, {Sc: 10001},
		{Nmax: 0}, {Nmax: 100001}, {Cmax: -1}, {Cmax: 1000001},
		{L: -1}, {Wb: 0}, {Q: -1}, {P: -1}, {P: 10001},
	}
	for i, c := range bad {
		if _, err := New(c); !errors.Is(err, errInvalidArgs) {
			t.Fatalf("bad cfg #%d accepted: %+v err=%v", i, c, err)
		}
	}
	s, _ := New(baseCfg())
	if _, err := s.Ingest(0, "", "a", 1, false); !errors.Is(err, errInvalidInput) {
		t.Fatal("empty traceID must fail")
	}
	if _, err := s.Ingest(0, "t", "a", 1e9+1, false); !errors.Is(err, errInvalidInput) {
		t.Fatal("durMs out of range must fail")
	}
	mustIngest(t, s, 5, "t", "a", 1, false)
	if _, err := s.Ingest(4, "t", "b", 1, false); !errors.Is(err, errClockRewind) {
		t.Fatal("clock rewind must fail")
	}
	if _, err := s.Ingest(6, "t", "a", 1, false); !errors.Is(err, errDuplicateSpan) {
		t.Fatal("duplicate buffered span must fail")
	}
	if ds, _ := s.Tick(15); len(ds) != 1 || ds[0].Spans != 1 || ds[0].At != 15 {
		t.Fatalf("rejected dup must not change lastSeen: %+v", ds)
	}
	t.Logf("input=dup then Tick15 output=decided@15 why=lastSeen stayed 5, 5+10<=15")
}

func TestCacheCapacityEviction(t *testing.T) {
	cfg := baseCfg()
	cfg.Cmax, cfg.Td = 1, 1000
	s, _ := New(cfg)
	mustIngest(t, s, 0, "a", "x", 1, true)
	mustIngest(t, s, 1, "b", "x", 1, true)
	ds, _ := s.Tick(11) // both decided (b silent at 1+10); capacity 1 evicts a
	if len(ds) != 2 {
		t.Fatalf("decisions=%+v", ds)
	}
	mustIngest(t, s, 12, "a", "y", 1, false) // "a" evicted from cache -> buffered fresh
	if st := s.Stats(); st.LateKept != 0 {
		t.Fatalf("evicted trace must not hit cache: %+v", st)
	}
	t.Logf("input=decide a,b Cmax=1 then late a output=buffered why=(0,a) evicted from cache")
}
