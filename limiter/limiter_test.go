package limiter_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/limiter"
	"ontology/rule"
)

func kv(pairs ...[2]string) []rule.KV {
	out := make([]rule.KV, len(pairs))
	for i, p := range pairs {
		out[i] = rule.KV{Key: p[0], Value: p[1]}
	}
	return out
}

func mustAdd(t *testing.T, l *limiter.Limiter, id string, pairs []rule.KV, limit, width int64, mode limiter.Mode) {
	t.Helper()
	if err := l.AddRule(id, pairs, limit, width, mode); err != nil {
		t.Fatalf("AddRule(%s): %v", id, err)
	}
}

func allow(t *testing.T, l *limiter.Limiter, desc []rule.KV, now int64) limiter.Result {
	t.Helper()
	res, err := l.Allow(desc, now)
	if err != nil {
		t.Fatalf("Allow(now=%d): %v", now, err)
	}
	return res
}

// Spec example: L=10, W=100, 8 hits in window 0, then requests at 150.
func TestSpecSlidingExample(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/pay"}), 10, 100, limiter.Enforce)
	desc := kv([2]string{"route", "/pay"})

	for i := 0; i < 8; i++ {
		allow(t, l, desc, int64(i))
	}
	// At now=150: est = cur + floor(8*50/100) = cur+4, so est runs 4..9.
	for i := 0; i < 6; i++ {
		if res := allow(t, l, desc, 150); !res.Allowed {
			t.Fatalf("request %d at 150 rejected, want allowed", i)
		}
	}
	if res := allow(t, l, desc, 150); res.Allowed || res.RejectedBy != "r" {
		t.Fatalf("7th request at 150 = %+v, want rejected by r", res)
	}
	// At now=199: est = 6 + floor(8*1/100) = 6, allowed again.
	if res := allow(t, l, desc, 199); !res.Allowed {
		t.Fatal("request at 199 rejected, want allowed")
	}
}

// Spec example: a shadow rule never rejects but records ShadowReject, and
// an enforce rejection freezes every counter and shadow statistic.
func TestSpecShadowExample(t *testing.T) {
	l := limiter.New()
	// Distinct key sets so both rules are selected for the same request.
	mustAdd(t, l, "s", kv([2]string{"route", "/p"}), 1, 100, limiter.Shadow)
	mustAdd(t, l, "r", kv([2]string{"tenant", "t"}), 2, 100, limiter.Enforce)
	desc := kv([2]string{"route", "/p"}, [2]string{"tenant", "t"})

	res := allow(t, l, desc, 0)
	if !res.Allowed || len(res.ShadowRejected) != 0 {
		t.Fatalf("now=0: %+v, want allowed with no shadow reject", res)
	}
	if !reflect.DeepEqual(res.Matched, []string{"r", "s"}) {
		t.Fatalf("now=0: matched = %v, want [r s]", res.Matched)
	}

	res = allow(t, l, desc, 1)
	if !res.Allowed || !reflect.DeepEqual(res.ShadowRejected, []string{"s"}) {
		t.Fatalf("now=1: %+v, want allowed with shadow reject [s]", res)
	}
	if got := l.ShadowReject("s"); got != 1 {
		t.Fatalf("ShadowReject(s) = %d, want 1", got)
	}

	// now=2: r's est=2, 2+1>2 rejects; s's counter and stats stay frozen.
	res = allow(t, l, desc, 2)
	if res.Allowed || res.RejectedBy != "r" || len(res.ShadowRejected) != 0 {
		t.Fatalf("now=2: %+v, want rejected by r with no shadow record", res)
	}
	if got := l.ShadowReject("s"); got != 1 {
		t.Fatalf("ShadowReject(s) after enforce reject = %d, want 1", got)
	}

	// SetMode keeps counters: s becomes enforce and immediately fails with
	// its carried count (est=2, 2+1>1).
	if err := l.SetMode("s", limiter.Enforce); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	res = allow(t, l, desc, 2)
	if res.Allowed || res.RejectedBy != "r" {
		t.Fatalf("now=2 after SetMode: %+v, want rejected (r is smallest id)", res)
	}
	if got := l.ShadowReject("s"); got != 1 {
		t.Fatalf("ShadowReject(s) after SetMode = %d, want 1", got)
	}
}

// Spec example: a wildcard rule counts each actual value independently.
func TestSpecWildcardPerValue(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "w", kv([2]string{"tenant", "*"}), 10, 100, limiter.Enforce)

	allow(t, l, kv([2]string{"tenant", "t1"}), 100)
	allow(t, l, kv([2]string{"tenant", "t2"}), 100)
	if got := l.Tracked(); got != 2 {
		t.Fatalf("Tracked() = %d, want 2", got)
	}

	// t1 reaches cur=6 inside window 1.
	for i := 0; i < 5; i++ {
		allow(t, l, kv([2]string{"tenant", "t1"}), 100)
	}
	// now=200 (k'=k+1): prev=6, cur=0, est = floor(6*100/100) = 6.
	res := allow(t, l, kv([2]string{"tenant", "t1"}), 200)
	if !res.Allowed {
		t.Fatal("t1 at 200 rejected, want allowed (est=6)")
	}
	// t2 is unaffected: its own counter is at cur=1.
	res = allow(t, l, kv([2]string{"tenant", "t2"}), 200)
	if !res.Allowed {
		t.Fatal("t2 at 200 rejected, want allowed")
	}

	// A fresh value at now=300 (k'>=k+2 for it) starts from est=0.
	res = allow(t, l, kv([2]string{"tenant", "t3"}), 300)
	if !res.Allowed {
		t.Fatal("t3 at 300 rejected, want allowed")
	}
	if got := l.Tracked(); got != 3 {
		t.Fatalf("Tracked() = %d, want 3", got)
	}
}

// est+1 == L allows; est+1 == L+1 rejects. Also covers k'>=k+2 reset.
func TestEstPlusOneBoundary(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 2, 100, limiter.Enforce)
	desc := kv([2]string{"route", "/p"})

	// est=0,1 allow; the third request has est=2, 2+1>2 rejects.
	allow(t, l, desc, 0)
	allow(t, l, desc, 1)
	if res := allow(t, l, desc, 2); res.Allowed || res.RejectedBy != "r" {
		t.Fatalf("est+1=L+1: %+v, want rejected by r", res)
	}
	// Rejection did not advance the counter: still full at now=3.
	if res := allow(t, l, desc, 3); res.Allowed {
		t.Fatal("rejected request must not have consumed quota")
	}
	// k'>=k+2 drops both windows: est=0 again.
	if res := allow(t, l, desc, 200); !res.Allowed {
		t.Fatal("after skipping two windows, want allowed")
	}
}

// Most-specific rule wins within a key set; different key sets stack.
func TestSelectionIntegration(t *testing.T) {
	l := limiter.New()
	// Same key set {tenant, route}: a and b tie at 1 exact pair (a wins by
	// id), c has 2 exact pairs and wins outright.
	mustAdd(t, l, "b", kv([2]string{"tenant", "vip"}, [2]string{"route", "*"}), 100, 100, limiter.Enforce)
	mustAdd(t, l, "a", kv([2]string{"tenant", "*"}, [2]string{"route", "/pay"}), 100, 100, limiter.Enforce)
	mustAdd(t, l, "c", kv([2]string{"tenant", "vip"}, [2]string{"route", "/pay"}), 100, 100, limiter.Enforce)
	// Different key set: stacks with the {tenant, route} winner.
	mustAdd(t, l, "z", kv([2]string{"region", "*"}), 100, 100, limiter.Enforce)
	desc := kv([2]string{"tenant", "vip"}, [2]string{"route", "/pay"}, [2]string{"region", "cn"})

	res := allow(t, l, desc, 0)
	if !reflect.DeepEqual(res.Matched, []string{"c", "z"}) {
		t.Fatalf("matched = %v, want [c z]", res.Matched)
	}
	if got := l.Tracked(); got != 2 {
		t.Fatalf("Tracked() = %d, want 2 (only selected rules count)", got)
	}
}

// A rejection advances no counter of any selected rule.
func TestRejectFreezesAllCounters(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "tight", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce)
	mustAdd(t, l, "loose", kv([2]string{"tenant", "t"}), 100, 100, limiter.Enforce)
	desc := kv([2]string{"route", "/p"}, [2]string{"tenant", "t"})

	allow(t, l, desc, 0) // both counters -> 1
	if res := allow(t, l, desc, 1); res.Allowed || res.RejectedBy != "tight" {
		t.Fatalf("now=1: %+v, want rejected by tight", res)
	}
	if got := l.Tracked(); got != 2 {
		t.Fatalf("Tracked() = %d, want 2 (no new counter on reject)", got)
	}
	// loose must still be at cur=1: after removing tight, one more request
	// at now=2 sees est=1 and is allowed.
	if err := l.RemoveRule("tight"); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
	if res := allow(t, l, desc, 2); !res.Allowed {
		t.Fatal("loose counter advanced despite the rejection")
	}
}

// RemoveRule deletes the rule's counters; re-adding starts fresh.
func TestRemoveRuleClearsCounters(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce)
	desc := kv([2]string{"route", "/p"})

	allow(t, l, desc, 0)
	if got := l.Tracked(); got != 1 {
		t.Fatalf("Tracked() = %d, want 1", got)
	}
	if err := l.RemoveRule("r"); err != nil {
		t.Fatalf("RemoveRule: %v", err)
	}
	if got := l.Tracked(); got != 0 {
		t.Fatalf("Tracked() after remove = %d, want 0", got)
	}
	if err := l.RemoveRule("r"); !errors.Is(err, limiter.ErrNotFound) {
		t.Fatalf("second RemoveRule = %v, want ErrNotFound", err)
	}
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce)
	if res := allow(t, l, desc, 1); !res.Allowed {
		t.Fatal("re-added rule must start with a fresh counter")
	}
}

// AddRule reports errors in the order: invalid argument > id exists >
// identical pattern.
func TestAddRuleErrorOrder(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce)

	cases := []struct {
		name  string
		id    string
		pairs []rule.KV
		limit int64
		width int64
		mode  limiter.Mode
		want  error
	}{
		{"empty id", "", kv([2]string{"a", "1"}), 1, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"empty pattern", "x", nil, 1, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"pattern too large", "x", kv([2]string{"a", "1"}, [2]string{"b", "2"}, [2]string{"c", "3"}, [2]string{"d", "4"}), 1, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"dup keys", "x", kv([2]string{"a", "1"}, [2]string{"a", "2"}), 1, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"empty value", "x", kv([2]string{"a", ""}), 1, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"zero limit", "x", kv([2]string{"a", "1"}), 0, 100, limiter.Enforce, limiter.ErrInvalidArgument},
		{"huge width", "x", kv([2]string{"a", "1"}), 1, 1_000_000_001, limiter.Enforce, limiter.ErrInvalidArgument},
		{"bad mode", "x", kv([2]string{"a", "1"}), 1, 100, limiter.Mode(7), limiter.ErrInvalidArgument},
		{"id exists beats pattern dup", "r", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce, limiter.ErrRuleExists},
		{"pattern dup", "s", kv([2]string{"route", "/p"}), 5, 500, limiter.Shadow, limiter.ErrPatternExists},
	}
	for _, tc := range cases {
		err := l.AddRule(tc.id, tc.pairs, tc.limit, tc.width, tc.mode)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	if got := l.Tracked(); got != 0 {
		t.Fatalf("rejected AddRule changed state: Tracked() = %d", got)
	}
}

// Allow reports errors in the order: invalid descriptor > invalid time >
// clock backwards; rejected calls do not advance maxNow.
func TestAllowErrorOrder(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 100, 100, limiter.Enforce)
	good := kv([2]string{"route", "/p"})

	cases := []struct {
		name string
		desc []rule.KV
		now  int64
		want error
	}{
		{"empty descriptor", nil, 0, limiter.ErrInvalidArgument},
		{"too many pairs", kv([2]string{"a", "1"}, [2]string{"b", "1"}, [2]string{"c", "1"}, [2]string{"d", "1"}, [2]string{"e", "1"}, [2]string{"f", "1"}, [2]string{"g", "1"}, [2]string{"h", "1"}, [2]string{"i", "1"}), 0, limiter.ErrInvalidArgument},
		{"dup keys", kv([2]string{"a", "1"}, [2]string{"a", "2"}), 0, limiter.ErrInvalidArgument},
		{"empty key", kv([2]string{"", "1"}), 0, limiter.ErrInvalidArgument},
		{"descriptor error beats time error", nil, -1, limiter.ErrInvalidArgument},
		{"negative now", good, -1, limiter.ErrInvalidTime},
		{"now too large", good, 1_000_000_000_000_001, limiter.ErrInvalidTime},
	}
	for _, tc := range cases {
		if _, err := l.Allow(tc.desc, tc.now); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}

	// Rejected calls did not move maxNow: now=0 is still accepted.
	if _, err := l.Allow(good, 0); err != nil {
		t.Fatalf("now=0 after rejected calls: %v", err)
	}
	// Time error beats clock-backwards: now=-1 is also < maxNow.
	if _, err := l.Allow(good, -1); !errors.Is(err, limiter.ErrInvalidTime) {
		t.Fatalf("now=-1: want ErrInvalidTime, got %v", err)
	}
	// A validated call advances maxNow even when it matches nothing.
	if _, err := l.Allow(kv([2]string{"other", "x"}), 50); err != nil {
		t.Fatalf("now=50: %v", err)
	}
	if _, err := l.Allow(good, 49); !errors.Is(err, limiter.ErrClockBackwards) {
		t.Fatalf("now=49: want ErrClockBackwards, got %v", err)
	}
	// The backwards call was rejected: now=50 still works.
	if _, err := l.Allow(good, 50); err != nil {
		t.Fatalf("now=50 after backwards call: %v", err)
	}
}

// No rule selected: allowed, no counter created.
func TestNoRuleSelected(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 1, 100, limiter.Enforce)
	res := allow(t, l, kv([2]string{"route", "/other"}), 0)
	if !res.Allowed || len(res.Matched) != 0 {
		t.Fatalf("res = %+v, want allowed with no match", res)
	}
	if got := l.Tracked(); got != 0 {
		t.Fatalf("Tracked() = %d, want 0", got)
	}
}

// Replaying the same operation sequence reproduces identical results.
func TestReplayDeterminism(t *testing.T) {
	run := func() []limiter.Result {
		l := limiter.New()
		mustAdd(t, l, "a", kv([2]string{"tenant", "*"}), 3, 100, limiter.Enforce)
		mustAdd(t, l, "b", kv([2]string{"route", "/p"}), 2, 50, limiter.Shadow)
		var out []limiter.Result
		for now := int64(0); now < 40; now++ {
			res, err := l.Allow(kv([2]string{"tenant", "t"}, [2]string{"route", "/p"}), now*7)
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			out = append(out, res)
		}
		return out
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replayed sequence diverged")
	}
}

// Concurrent calls behave like some serial order: no race, no panic, and
// the number of allowed requests never exceeds what any serial execution
// of the same calls could allow.
func TestConcurrentSmoke(t *testing.T) {
	l := limiter.New()
	mustAdd(t, l, "r", kv([2]string{"route", "/p"}), 64, 1000, limiter.Enforce)
	mustAdd(t, l, "s", kv([2]string{"tenant", "*"}), 1, 1000, limiter.Shadow)
	desc := kv([2]string{"route", "/p"}, [2]string{"tenant", "t"})

	var wg sync.WaitGroup
	allowed := make(chan bool, 4096)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 128; i++ {
				now := int64(g*128 + i)
				res, err := l.Allow(desc, now)
				if err != nil && !errors.Is(err, limiter.ErrClockBackwards) {
					t.Errorf("Allow: %v", err)
					return
				}
				if err == nil {
					allowed <- res.Allowed
				}
			}
		}(g)
	}
	wg.Wait()
	close(allowed)
	n := 0
	for a := range allowed {
		if a {
			n++
		}
	}
	// Any serial order of validated calls at times < 1024 allows at most
	// L + one full previous window of weight.
	if n > 128 {
		t.Fatalf("allowed %d requests, impossibly many for L=64", n)
	}
	if got := l.Tracked(); got != 2 {
		t.Fatalf("Tracked() = %d, want 2", got)
	}
	t.Logf("concurrent run allowed %d requests", n)
}
