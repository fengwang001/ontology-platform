package cache

import (
	"flag"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

var randomSeed = flag.Int64("random-seed", 0, "fixed seed for the differential test")

const randomCases = 2000

// plannedResult is what the shared origin script returns for one fetch call.
type plannedResult struct {
	status int
	vis    Visibility
	ttl    int64
	vary   []string
	size   int64
	body   string
	err    error
}

type originScript struct {
	calls  int
	plan   []plannedResult
	negTTL bool
	negSz  bool
}

func (o *originScript) fetch(req Request) (*FetchResult, error) {
	idx := o.calls
	o.calls++
	if idx >= len(o.plan) {
		return &FetchResult{Status: 500, Vis: NoStore, TTL: 1, Size: 1}, nil
	}
	p := o.plan[idx]
	if p.err != nil {
		return nil, p.err
	}
	ttl := p.ttl
	size := p.size
	if o.negTTL {
		ttl = -1
	}
	if o.negSz {
		size = -1
	}
	return &FetchResult{
		Status: p.status, Vis: p.vis, TTL: ttl, Vary: p.vary,
		Size: size, Body: []byte(p.body),
	}, nil
}

func TestRandomDifferential(t *testing.T) {
	var baseSeed int64 = 1
	if *randomSeed != 0 {
		baseSeed = *randomSeed
	}
	for tc := 0; tc < randomCases; tc++ {
		seed := baseSeed*100000 + int64(tc)
		rng := rand.New(rand.NewSource(seed))
		runOneDifferential(t, seed, rng)
	}
}

func runOneDifferential(t *testing.T, seed int64, rng *rand.Rand) {
	t.Helper()
	capBytes := int64(rng.Intn(120) + 1)
	v := rng.Intn(3) + 1
	real := New(capBytes, v)
	model := newModel(capBytes, v)

	paths := []string{"/a", "/b", "/admin", "/admin/x", "/adminx", "/me", "/p"}
	subjects := []string{"", "u1", "u2", "u3"}
	headers := []string{"accept-language", "x-token", "authorization", "cookie", "other"}
	values := []string{"", "en", "fr", "tok", "zz"}

	var logBuf strings.Builder
	fmt.Fprintf(&logBuf, "seed=%d cap=%d v=%d\n", seed, capBytes, v)

	steps := 40 + rng.Intn(30)
	now := int64(0)
	for step := 0; step < steps; step++ {
		// Occasionally add a policy.
		if rng.Intn(12) == 0 {
			prefix := []string{"/admin", "/admin/x", "/p", "bad", ""}[rng.Intn(5)]
			scope := []string{"admin", "user", ""}[rng.Intn(3)]
			gotErr := real.AddPolicy(prefix, scope)
			okModel := model.addPolicy(prefix, scope)
			fmt.Fprintf(&logBuf, "step %d AddPolicy(%q,%q) ok=%v\n", step, prefix, scope, gotErr == nil)
			if (gotErr == nil) != okModel {
				t.Fatalf("seed=%d policy mismatch: real=%v model=%v\n%s", seed, gotErr, okModel, logBuf.String())
			}
			continue
		}

		methods := []string{"GET", "GET", "GET", "POST", "PUT", "PATCH", "DELETE"}
		method := methods[rng.Intn(len(methods))]
		path := paths[rng.Intn(len(paths))]
		subject := subjects[rng.Intn(len(subjects))]
		var scopes []string
		if rng.Intn(2) == 0 {
			scopes = []string{"admin"}
		}
		head := map[string]string{}
		nh := rng.Intn(3)
		for i := 0; i < nh; i++ {
			name := headers[rng.Intn(len(headers))]
			head[name] = values[rng.Intn(len(values))]
		}
		req := Request{Method: method, Path: path, Subject: subject, Scopes: scopes, Headers: head}

		// non-monotonic time sometimes, occasionally way out of range
		switch rng.Intn(10) {
		case 0:
			if now > 0 {
				now -= int64(rng.Intn(3) + 1)
			}
		case 9:
			now = 1000000000000001
		default:
			now += int64(rng.Intn(6))
		}

		// Plan one fetch result, shared by both implementations.
		plan := randomPlan(rng, capBytes)
		scriptReal := &originScript{plan: []plannedResult{plan}, negTTL: false, negSz: false}
		scriptModel := &originScript{plan: []plannedResult{plan}, negTTL: false, negSz: false}

		rRes, rErr := real.Get(req, now, scriptReal.fetch)
		mRes := model.get(req, now, scriptModel.fetch)

		fmt.Fprintf(&logBuf, "step %d %s %s subj=%q scopes=%v head=%v now=%d fetch=%v\n",
			step, method, path, subject, scopes, head, now, formatPlan(plan))

		assertOutcomeEqual(t, seed, step, rRes, rErr, mRes, logBuf.String())
		assertStatsEqual(t, seed, step, real, model, logBuf.String())
	}

	// Emit the full replay log at low verbosity only on failure; with -v the
	// last seed's log is always shown for reproducibility.
	if testing.Verbose() {
		t.Logf("\n%s", logBuf.String())
	}
}

func randomPlan(rng *rand.Rand, capBytes int64) plannedResult {
	if rng.Intn(8) == 0 {
		return plannedResult{err: errBoom}
	}
	statusPool := []int{200, 200, 204, 299, 300, 301, 400, 404, 500}
	status := statusPool[rng.Intn(len(statusPool))]
	vis := []Visibility{Public, Public, Public, Private, NoStore}[rng.Intn(5)]
	ttlPool := []int64{0, 1, 2, 5, 20, 100}
	ttl := ttlPool[rng.Intn(len(ttlPool))]
	size := int64(rng.Intn(int(capBytes) + 30))
	var vary []string
	switch rng.Intn(4) {
	case 0:
		vary = []string{"accept-language"}
	case 1:
		vary = []string{"x-token", "accept-language"}
	case 2:
		vary = []string{"*"}
	}
	return plannedResult{
		status: status, vis: vis, ttl: ttl, vary: vary, size: size,
		body: fmt.Sprintf("b%d", rng.Intn(5)),
	}
}

func formatPlan(p plannedResult) string {
	if p.err != nil {
		return "error"
	}
	vis := map[Visibility]string{Public: "PUB", Private: "PRIV", NoStore: "NO"}[p.vis]
	return fmt.Sprintf("(%d %s ttl=%d vary=%v size=%d)", p.status, vis, p.ttl, p.vary, p.size)
}

func assertOutcomeEqual(t *testing.T, seed int64, step int,
	rRes *Result, rErr error, mRes modelOutcome, log string) {
	t.Helper()
	if (rErr != nil) != (mRes.err != nil) {
		t.Fatalf("seed=%d step=%d err mismatch real=%v model=%v\n%s", seed, step, rErr, mRes.err, log)
	}
	if rErr != nil {
		if rErr.Error() != mRes.err.Error() {
			t.Fatalf("seed=%d step=%d err value real=%v model=%v\n%s", seed, step, rErr, mRes.err, log)
		}
		return
	}
	if rRes.Status != mRes.status || string(rRes.Body) != string(mRes.body) ||
		rRes.Source != mRes.source {
		t.Fatalf("seed=%d step=%d result mismatch real=(%d %q %s) model=(%d %q %s)\n%s",
			seed, step,
			rRes.Status, rRes.Body, rRes.Source,
			mRes.status, mRes.body, mRes.source, log)
	}
}

func assertStatsEqual(t *testing.T, seed int64, step int, real *Cache, model *modelCache, log string) {
	t.Helper()
	s := real.Stats()
	if s.Fetches != model.fetches {
		t.Fatalf("seed=%d step=%d fetches real=%d model=%d\n%s", seed, step, s.Fetches, model.fetches, log)
	}
	if s.Hits != model.hits || s.Misses != model.misses || s.Bypasses != model.bypasses {
		t.Fatalf("seed=%d step=%d sources real=(h%d m%d b%d) model=(h%d m%d b%d)\n%s",
			seed, step, s.Hits, s.Misses, s.Bypasses, model.hits, model.misses, model.bypasses, log)
	}
	if s.Invalidated != model.inval {
		t.Fatalf("seed=%d step=%d invalidated real=%d model=%d\n%s",
			seed, step, s.Invalidated, model.inval, log)
	}
	if real.Bytes() != model.bytes {
		t.Fatalf("seed=%d step=%d bytes real=%d model=%d\n%s",
			seed, step, real.Bytes(), model.bytes, log)
	}
	if real.Bytes() > real.cap {
		t.Fatalf("seed=%d step=%d invariant bytes=%d > cap=%d\n%s",
			seed, step, real.Bytes(), real.cap, log)
	}
	for path, variants := range model.variants {
		if len(variants) > model.v {
			t.Fatalf("seed=%d step=%d path %q has %d > %d variants\n%s",
				seed, step, path, len(variants), model.v, log)
		}
	}
	if len(s.Evictions) != len(model.evicts) {
		t.Fatalf("seed=%d step=%d eviction count real=%d model=%d\nreal=%+v\nmodel=%+v\n%s",
			seed, step, len(s.Evictions), len(model.evicts), s.Evictions, model.evicts, log)
	}
	for i := range s.Evictions {
		if s.Evictions[i] != model.evicts[i] {
			t.Fatalf("seed=%d step=%d eviction %d real=%+v model=%+v\n%s",
				seed, step, i, s.Evictions[i], model.evicts[i], log)
		}
	}
}
