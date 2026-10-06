package cors

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// 本文件是与 Engine 独立编写的朴素参考模型：缓存用切片线性扫描、
// 分类用嵌套循环，语义与规格一致但实现路径完全不同，
// 用于随机操作序列的对照验证。

type naiveEntry struct {
	origin, target string
	creds          bool
	methods        []string
	headers        []string
	anyM, anyH     bool
	expiry         int64
	lastHit        int64
	created        uint64
}

type naiveModel struct {
	cfg     Config
	now     int64
	entries []*naiveEntry
	seq     uint64
	opaque  uint64
}

func newNaive(cfg Config) *naiveModel { return &naiveModel{cfg: cfg} }

const tokenChars = "!#$%&'*+-.^_`|~"

func naiveNorm(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, r := range name {
		ok := ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') ||
			strings.ContainsRune(tokenChars, r)
		if !ok {
			return "", false
		}
	}
	return strings.ToLower(name), true
}

func (m *naiveModel) validate(r Request) *Error {
	if !r.Origin.opaque && r.Origin.value == "" {
		return invalidArg("empty origin")
	}
	if r.Target == "" {
		return invalidArg("empty target")
	}
	if r.Method == "" {
		return invalidArg("empty method")
	}
	for _, h := range r.Headers {
		if _, ok := naiveNorm(h.Name); !ok {
			return invalidArg("invalid header name %q", h.Name)
		}
	}
	return nil
}

func (m *naiveModel) isSafeMethod(method string) bool {
	for _, s := range m.cfg.SafeMethods {
		if s == method {
			return true
		}
	}
	return false
}

func (m *naiveModel) safeRule(name string) (HeaderRule, bool) {
	for k, rule := range m.cfg.SafeHeaders {
		kn, ok := naiveNorm(k)
		if ok && kn == name {
			return rule, true
		}
	}
	return HeaderRule{}, false
}

func (m *naiveModel) isSimple(r Request) bool {
	if !m.isSafeMethod(r.Method) {
		return false
	}
	for _, h := range r.Headers {
		nn, ok := naiveNorm(h.Name)
		if !ok {
			return false
		}
		rule, ok := m.safeRule(nn)
		if !ok || len(h.Value) > rule.MaxLength {
			return false
		}
		for i := 0; i < len(h.Value); i++ {
			if !strings.ContainsRune(rule.AllowedChars, rune(h.Value[i])) {
				return false
			}
		}
	}
	return true
}

func (m *naiveModel) nonSafe(r Request) []string {
	var out []string
	for _, h := range r.Headers {
		nn, ok := naiveNorm(h.Name)
		if !ok {
			continue
		}
		if _, safe := m.safeRule(nn); safe {
			continue
		}
		dup := false
		for _, seen := range out {
			if seen == nn {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, nn)
		}
	}
	sort.Strings(out)
	return out
}

func (m *naiveModel) find(originKey, target string, creds bool) *naiveEntry {
	for _, e := range m.entries {
		if e.origin == originKey && e.target == target && e.creds == creds {
			return e
		}
	}
	return nil
}

func naiveAllows(e *naiveEntry, method string, nonSafe []string) bool {
	if !e.anyM {
		found := false
		for _, mm := range e.methods {
			if mm == method {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if !e.anyH {
		for _, h := range nonSafe {
			found := false
			for _, eh := range e.headers {
				if eh == h {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

func (m *naiveModel) decide(r Request) (Decision, *Error) {
	if err := m.validate(r); err != nil {
		return Decision{}, err
	}
	if !r.Origin.opaque && r.Origin.value == r.Target {
		return Decision{Kind: DecisionSameOrigin}, nil
	}
	if m.isSimple(r) {
		return Decision{Kind: DecisionSimple}, nil
	}
	key := r.Origin.cacheKey()
	if e := m.find(key, r.Target, r.IncludeCredentials); e != nil {
		if m.now < e.expiry && naiveAllows(e, r.Method, m.nonSafe(r)) {
			e.lastHit = m.now
			return Decision{Kind: DecisionCacheHit}, nil
		}
	}
	return Decision{Kind: DecisionPreflightNeeded}, nil
}

func naiveOriginAllowed(r Request, allowOrigin string) bool {
	if allowOrigin == "*" {
		return !r.IncludeCredentials
	}
	if r.Origin.opaque {
		return false
	}
	return allowOrigin == r.Origin.value
}

func (m *naiveModel) maxAge(declared *int64) (int64, bool) {
	age := m.cfg.DefaultMaxAge
	if declared != nil {
		if *declared <= 0 {
			return 0, false
		}
		age = *declared
	}
	if age > m.cfg.MaxMaxAge {
		age = m.cfg.MaxMaxAge
	}
	if age <= 0 {
		return 0, false
	}
	return age, true
}

func (m *naiveModel) evict() {
	if len(m.entries) <= m.cfg.Capacity {
		return
	}
	var kept []*naiveEntry
	for _, e := range m.entries {
		if e.expiry > m.now {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	for len(m.entries) > m.cfg.Capacity {
		victim := 0
		for i, e := range m.entries {
			v := m.entries[victim]
			if e.lastHit < v.lastHit || (e.lastHit == v.lastHit && e.created < v.created) {
				victim = i
			}
		}
		m.entries = append(m.entries[:victim], m.entries[victim+1:]...)
	}
}

func (m *naiveModel) submitPreflight(r Request, resp PreflightResponse) *Error {
	if err := m.validate(r); err != nil {
		return err
	}
	var allowHeaders []string
	for _, h := range resp.AllowHeaders {
		nn, ok := naiveNorm(h)
		if !ok {
			return invalidArg("invalid header name %q in response", h)
		}
		allowHeaders = append(allowHeaders, nn)
	}
	if resp.Redirected {
		return &Error{Kind: ErrKindRedirectNotAllowed, Message: "preflight must not be redirected"}
	}
	if !naiveOriginAllowed(r, resp.AllowOrigin) {
		return preflightErr(ReasonOriginMismatch, "allow-origin %q does not match origin", resp.AllowOrigin)
	}
	if r.IncludeCredentials && !resp.AllowCredentials {
		return preflightErr(ReasonCredentialsMismatch, "credentials included but not allowed")
	}
	anyM := resp.AllowAnyMethod && !r.IncludeCredentials
	methodOK := anyM
	for _, mm := range resp.AllowMethods {
		if mm == r.Method {
			methodOK = true
		}
	}
	if !methodOK {
		return preflightErr(ReasonMethodMismatch, "method %q not allowed", r.Method)
	}
	anyH := resp.AllowAnyHeader && !r.IncludeCredentials
	if !anyH {
		for _, need := range m.nonSafe(r) {
			found := false
			for _, ah := range allowHeaders {
				if ah == need {
					found = true
				}
			}
			if !found {
				return preflightErr(ReasonHeaderMismatch, "header %q not allowed", need)
			}
		}
	}
	age, cacheable := m.maxAge(resp.MaxAge)
	if !cacheable {
		return nil
	}
	expiry := m.now + age
	key := r.Origin.cacheKey()
	if e := m.find(key, r.Target, r.IncludeCredentials); e != nil && m.now < e.expiry {
		for _, mm := range resp.AllowMethods {
			dup := false
			for _, em := range e.methods {
				if em == mm {
					dup = true
				}
			}
			if !dup {
				e.methods = append(e.methods, mm)
			}
		}
		for _, hh := range allowHeaders {
			dup := false
			for _, eh := range e.headers {
				if eh == hh {
					dup = true
				}
			}
			if !dup {
				e.headers = append(e.headers, hh)
			}
		}
		e.anyM = anyM
		e.anyH = anyH
		e.expiry = expiry
		return nil
	}
	m.seq++
	e := &naiveEntry{
		origin: key, target: r.Target, creds: r.IncludeCredentials,
		anyM: anyM, anyH: anyH, expiry: expiry, lastHit: m.now, created: m.seq,
	}
	for _, mm := range resp.AllowMethods {
		dup := false
		for _, em := range e.methods {
			if em == mm {
				dup = true
			}
		}
		if !dup {
			e.methods = append(e.methods, mm)
		}
	}
	for _, hh := range allowHeaders {
		dup := false
		for _, eh := range e.headers {
			if eh == hh {
				dup = true
			}
		}
		if !dup {
			e.headers = append(e.headers, hh)
		}
	}
	var kept []*naiveEntry
	for _, old := range m.entries {
		if !(old.origin == key && old.target == r.Target && old.creds == r.IncludeCredentials) {
			kept = append(kept, old)
		}
	}
	m.entries = kept
	m.entries = append(m.entries, e)
	m.evict()
	return nil
}

func (m *naiveModel) submitActual(r Request, resp ActualResponse) (ActualResult, *Error) {
	if err := m.validate(r); err != nil {
		return ActualResult{}, err
	}
	var expose []string
	for _, h := range resp.ExposeHeaders {
		nn, ok := naiveNorm(h)
		if !ok {
			return ActualResult{}, invalidArg("invalid header name %q in response", h)
		}
		expose = append(expose, nn)
	}
	if resp.RedirectTo != "" {
		fu := r
		fu.Target = resp.RedirectTo
		if resp.RedirectTo != r.Target {
			m.opaque++
			fu.Origin = opaqueOrigin(m.opaque)
		}
		return ActualResult{Redirect: true, FollowUp: fu}, nil
	}
	if !naiveOriginAllowed(r, resp.AllowOrigin) {
		return ActualResult{}, &Error{Kind: ErrKindResponseValidationFailed, Reason: ReasonOriginMismatch,
			Message: "allow-origin does not match origin"}
	}
	if r.IncludeCredentials && !resp.AllowCredentials {
		return ActualResult{}, &Error{Kind: ErrKindResponseValidationFailed, Reason: ReasonCredentialsMismatch,
			Message: "credentials included but not allowed"}
	}
	wildcard := resp.ExposeAnyHeader && !r.IncludeCredentials
	var out []Header
	for _, h := range resp.Headers {
		nn, ok := naiveNorm(h.Name)
		if !ok {
			continue
		}
		exposed := wildcard
		for _, s := range m.cfg.SafeResponseHeaders {
			if sn, ok := naiveNorm(s); ok && sn == nn {
				exposed = true
			}
		}
		for _, x := range expose {
			if x == nn {
				exposed = true
			}
		}
		if exposed {
			out = append(out, h)
		}
	}
	return ActualResult{Exposed: out}, nil
}

func (m *naiveModel) advance(delta int64) *Error {
	if delta < 0 {
		return &Error{Kind: ErrKindClockRollback, Message: "negative delta"}
	}
	m.now += delta
	return nil
}

func (m *naiveModel) purgeOrigin(o Origin) int {
	key := o.cacheKey()
	var kept []*naiveEntry
	n := 0
	for _, e := range m.entries {
		if e.origin == key {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	return n
}

func (m *naiveModel) purgeTarget(target string) int {
	var kept []*naiveEntry
	n := 0
	for _, e := range m.entries {
		if e.target == target {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	return n
}

func (m *naiveModel) snapshot() []EntrySnapshot {
	out := make([]EntrySnapshot, 0, len(m.entries))
	for _, e := range m.entries {
		snap := EntrySnapshot{
			Origin: e.origin, Target: e.target, Credentials: e.creds,
			AnyMethod: e.anyM, AnyHeader: e.anyH, Expiry: e.expiry,
		}
		snap.Methods = append(snap.Methods, e.methods...)
		snap.Headers = append(snap.Headers, e.headers...)
		sort.Strings(snap.Methods)
		sort.Strings(snap.Headers)
		out = append(out, snap)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin < out[j].Origin
		}
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		return !out[i].Credentials && out[j].Credentials
	})
	return out
}

var (
	randOrigins     = []string{"https://a", "https://b", "https://c"}
	randMethods     = []string{"GET", "POST", "HEAD", "PUT", "DELETE"}
	randHeaderNames = []string{"x-a", "X-A", "x-b", "X-B", "x-c", "content-type", "X-Q", "bad name"}
	randHeaderVals  = []string{"", "a", "ab", "abc", "abcd", "a1", "zzzzzz", "a b"}
)

func randomConfig(rng *rand.Rand) Config {
	cfg := Config{
		DefaultMaxAge: int64(rng.Intn(4)),
		MaxMaxAge:     int64(1 + rng.Intn(10)),
		Capacity:      1 + rng.Intn(6),
	}
	for _, m := range []string{"GET", "POST", "HEAD"} {
		if rng.Intn(2) == 0 {
			cfg.SafeMethods = append(cfg.SafeMethods, m)
		}
	}
	cfg.SafeHeaders = map[string]HeaderRule{}
	charsets := []string{"ab", "abc", "0123", "abcABC123"}
	for _, h := range []string{"x-a", "x-b", "x-c", "content-type"} {
		if rng.Intn(2) == 0 {
			cfg.SafeHeaders[h] = HeaderRule{
				MaxLength:    1 + rng.Intn(6),
				AllowedChars: charsets[rng.Intn(len(charsets))],
			}
		}
	}
	for _, h := range []string{"content-type", "x-r"} {
		if rng.Intn(2) == 0 {
			cfg.SafeResponseHeaders = append(cfg.SafeResponseHeaders, h)
		}
	}
	return cfg
}

func randomRequest(rng *rand.Rand, followUps []Request) Request {
	if len(followUps) > 0 && rng.Intn(10) == 0 {
		return followUps[rng.Intn(len(followUps))]
	}
	r := Request{
		Origin:             NewOrigin(randOrigins[rng.Intn(len(randOrigins))]),
		Target:             randOrigins[rng.Intn(len(randOrigins))],
		Method:             randMethods[rng.Intn(len(randMethods))],
		IncludeCredentials: rng.Intn(10) < 3,
	}
	if rng.Intn(40) == 0 {
		r.Origin = NewOrigin("")
	}
	if rng.Intn(50) == 0 {
		r.Method = ""
	}
	for i, n := 0, rng.Intn(4); i < n; i++ {
		r.Headers = append(r.Headers, Header{
			Name:  randHeaderNames[rng.Intn(len(randHeaderNames))],
			Value: randHeaderVals[rng.Intn(len(randHeaderVals))],
		})
	}
	return r
}

func randomPreflightResponse(rng *rand.Rand, r Request) PreflightResponse {
	resp := PreflightResponse{
		Redirected:       rng.Intn(25) == 0,
		AllowCredentials: rng.Intn(2) == 0,
		AllowAnyMethod:   rng.Intn(4) == 0,
		AllowAnyHeader:   rng.Intn(4) == 0,
	}
	switch rng.Intn(4) {
	case 0:
		resp.AllowOrigin = r.Origin.Value()
	case 1:
		resp.AllowOrigin = "*"
	case 2:
		resp.AllowOrigin = "https://other"
	}
	for _, m := range randMethods {
		if rng.Intn(3) == 0 || m == r.Method && rng.Intn(2) == 0 {
			resp.AllowMethods = append(resp.AllowMethods, m)
		}
	}
	for _, h := range []string{"x-a", "x-b", "x-c", "x-q", "X-A"} {
		if rng.Intn(3) == 0 {
			resp.AllowHeaders = append(resp.AllowHeaders, h)
		}
	}
	if rng.Intn(10) < 3 {
		resp.MaxAge = nil
	} else {
		ages := []int64{-2, 0, 1, 2, 5, 50}
		resp.MaxAge = i64(ages[rng.Intn(len(ages))])
	}
	return resp
}

func randomActualResponse(rng *rand.Rand, r Request) ActualResponse {
	resp := ActualResponse{
		AllowCredentials: rng.Intn(2) == 0,
		ExposeAnyHeader:  rng.Intn(4) == 0,
	}
	switch rng.Intn(4) {
	case 0:
		resp.AllowOrigin = r.Origin.Value()
	case 1:
		resp.AllowOrigin = "*"
	case 2:
		resp.AllowOrigin = "https://other"
	}
	if rng.Intn(10) == 0 {
		resp.RedirectTo = randOrigins[rng.Intn(len(randOrigins))]
	}
	for _, h := range []string{"x-a", "x-r", "x-secret"} {
		if rng.Intn(3) == 0 {
			resp.ExposeHeaders = append(resp.ExposeHeaders, h)
		}
	}
	for i, n := 0, rng.Intn(4); i < n; i++ {
		names := []string{"content-type", "x-r", "x-secret", "X-A"}
		resp.Headers = append(resp.Headers, Header{
			Name:  names[rng.Intn(len(names))],
			Value: randHeaderVals[rng.Intn(len(randHeaderVals))],
		})
	}
	return resp
}

func sameErr(a, b *Error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Kind == b.Kind && a.Reason == b.Reason
}

func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261006))
	for trial := 0; trial < 30; trial++ {
		cfg := randomConfig(rng)
		eng, err := NewEngine(cfg)
		if err != nil {
			t.Fatalf("trial %d: NewEngine: %v", trial, err)
		}
		var logs []string
		eng.SetLogger(func(s string) { logs = append(logs, s) })
		model := newNaive(cfg)
		var followUps []Request
		fail := func(step int, format string, args ...any) {
			msg := fmt.Sprintf(format, args...)
			start := len(logs) - 10
			if start < 0 {
				start = 0
			}
			t.Fatalf("trial %d step %d: %s\nrecent logs:\n%s",
				trial, step, msg, strings.Join(logs[start:], "\n"))
		}
		for step := 0; step < 400; step++ {
			switch rng.Intn(10) {
			case 0, 1, 2:
				r := randomRequest(rng, followUps)
				d1, e1 := eng.Decide(r)
				d2, e2 := model.decide(r)
				if !sameErr(e1, e2) {
					fail(step, "decide err mismatch: engine=%v model=%v req=%+v", e1, e2, r)
				}
				if e1 == nil && d1.Kind != d2.Kind {
					fail(step, "decide kind mismatch: engine=%v model=%v req=%+v", d1.Kind, d2.Kind, r)
				}
			case 3, 4, 5:
				r := randomRequest(rng, followUps)
				resp := randomPreflightResponse(rng, r)
				e1 := eng.SubmitPreflight(r, resp)
				e2 := model.submitPreflight(r, resp)
				if !sameErr(e1, e2) {
					fail(step, "preflight err mismatch: engine=%v model=%v req=%+v resp=%+v", e1, e2, r, resp)
				}
			case 6, 7:
				r := randomRequest(rng, followUps)
				resp := randomActualResponse(rng, r)
				a1, e1 := eng.SubmitActual(r, resp)
				a2, e2 := model.submitActual(r, resp)
				if !sameErr(e1, e2) {
					fail(step, "actual err mismatch: engine=%v model=%v req=%+v resp=%+v", e1, e2, r, resp)
				}
				if e1 == nil {
					if a1.Redirect != a2.Redirect ||
						a1.FollowUp.Target != a2.FollowUp.Target ||
						a1.FollowUp.Origin.cacheKey() != a2.FollowUp.Origin.cacheKey() ||
						!reflect.DeepEqual(a1.Exposed, a2.Exposed) {
						fail(step, "actual result mismatch: engine=%+v model=%+v", a1, a2)
					}
					if a1.Redirect {
						followUps = append(followUps, a1.FollowUp)
						if len(followUps) > 8 {
							followUps = followUps[1:]
						}
					}
				}
			case 8:
				delta := int64(rng.Intn(6))
				if rng.Intn(20) == 0 {
					delta = -delta
				}
				e1 := eng.AdvanceClock(delta)
				e2 := model.advance(delta)
				if !sameErr(e1, e2) {
					fail(step, "advance err mismatch: engine=%v model=%v", e1, e2)
				}
			case 9:
				if rng.Intn(2) == 0 {
					o := NewOrigin(randOrigins[rng.Intn(len(randOrigins))])
					if n1, n2 := eng.PurgeByOrigin(o), model.purgeOrigin(o); n1 != n2 {
						fail(step, "purge origin mismatch: engine=%d model=%d", n1, n2)
					}
				} else {
					target := randOrigins[rng.Intn(len(randOrigins))]
					if n1, n2 := eng.PurgeByTarget(target), model.purgeTarget(target); n1 != n2 {
						fail(step, "purge target mismatch: engine=%d model=%d", n1, n2)
					}
				}
			}
			if n := eng.EntryCount(); n > cfg.Capacity {
				fail(step, "entry count %d exceeds capacity %d", n, cfg.Capacity)
			}
			if eng.Now() != model.now {
				fail(step, "clock mismatch: engine=%d model=%d", eng.Now(), model.now)
			}
			if s1, s2 := eng.Snapshot(), model.snapshot(); !reflect.DeepEqual(s1, s2) {
				fail(step, "cache snapshot mismatch:\nengine=%+v\nmodel=%+v", s1, s2)
			}
		}
	}
}
