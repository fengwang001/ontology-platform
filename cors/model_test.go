package cors

// 本文件包含一个独立编写的朴素参照模型：刻意使用切片与线性扫描实现，
// 与内核的哈希表/互斥锁实现相互独立，用于随机操作序列的差分对照。

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

type mEntry struct {
	origin, target string
	creds          CredentialsMode
	methods        []string
	headers        []string
	anyM, anyH     bool
	exp, lastHit   int64
	created        int64
}

type model struct {
	cfg     Config
	now     int64
	seq     int64
	opaque  int64
	entries []*mEntry
}

func newModel(cfg Config) *model { return &model{cfg: cfg} }

func mNorm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func mTokenChar(b byte) bool {
	if '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' {
		return true
	}
	return strings.IndexByte("!#$%&'*+-.^_`|~", b) >= 0
}

func mContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (m *model) validate(req Request) *Error {
	if req.Origin == "" || req.Target == "" || req.Method == "" {
		return &Error{Code: ErrCodeInvalidArgument, Msg: "empty origin/target/method"}
	}
	for _, h := range req.Headers {
		if h.Name == "" {
			return &Error{Code: ErrCodeInvalidArgument, Msg: "empty header name"}
		}
		for i := 0; i < len(h.Name); i++ {
			if !mTokenChar(h.Name[i]) {
				return &Error{Code: ErrCodeInvalidArgument, Msg: "illegal header name"}
			}
		}
	}
	return nil
}

func (m *model) classify(req Request) (bool, []string) {
	simple := false
	for _, sm := range m.cfg.SafeMethods {
		if sm == req.Method {
			simple = true
			break
		}
	}
	var nonSafe []string
	seen := map[string]bool{}
	for _, h := range req.Headers {
		name := mNorm(h.Name)
		var rule HeaderRule
		ok := false
		for cfgName, cfgRule := range m.cfg.SafeHeaders {
			if mNorm(cfgName) == name {
				rule, ok = cfgRule, true
				break
			}
		}
		if !ok {
			if !seen[name] {
				seen[name] = true
				nonSafe = append(nonSafe, name)
			}
			simple = false
			continue
		}
		if len(h.Value) > rule.MaxLen {
			simple = false
			continue
		}
		if rule.Allowed != nil {
			for i := 0; i < len(h.Value); i++ {
				if !rule.Allowed(h.Value[i]) {
					simple = false
					break
				}
			}
		}
	}
	return simple, nonSafe
}

func (m *model) get(origin, target string, creds CredentialsMode) *mEntry {
	for _, e := range m.entries {
		if e.origin == origin && e.target == target && e.creds == creds && e.exp > m.now {
			return e
		}
	}
	return nil
}

func mCovers(e *mEntry, method string, nonSafe []string) bool {
	if !e.anyM && !mContains(e.methods, method) {
		return false
	}
	for _, h := range nonSafe {
		if !e.anyH && !mContains(e.headers, h) {
			return false
		}
	}
	return true
}

func (m *model) decide(req Request) (Decision, *Error) {
	if err := m.validate(req); err != nil {
		return Decision{}, err
	}
	simple, nonSafe := m.classify(req)
	if simple {
		return Decision{Action: ActionSend, Simple: true}, nil
	}
	if e := m.get(req.Origin, req.Target, req.Credentials); e != nil && mCovers(e, req.Method, nonSafe) {
		e.lastHit = m.now
		return Decision{Action: ActionSend, CacheHit: true, NonSafeHeaders: nonSafe}, nil
	}
	return Decision{Action: ActionPreflight, NonSafeHeaders: nonSafe}, nil
}

func mSplitList(v string) []string {
	var out []string
	for _, it := range strings.Split(v, ",") {
		if it = strings.TrimSpace(it); it != "" {
			out = append(out, it)
		}
	}
	return out
}

func (m *model) preflight(req Request, resp PreflightResponse) *Error {
	if err := m.validate(req); err != nil {
		return err
	}
	if resp.Redirect {
		return &Error{Code: ErrCodeRedirectNotAllowed, Msg: "preflight redirected"}
	}
	h := map[string]string{}
	for n, v := range resp.Headers {
		h[mNorm(n)] = v
	}
	include := req.Credentials == CredentialsInclude
	allowOrigin := strings.TrimSpace(h["access-control-allow-origin"])
	allowCreds := strings.EqualFold(strings.TrimSpace(h["access-control-allow-credentials"]), "true")
	var methods, headers []string
	anyM, anyH := false, false
	for _, it := range mSplitList(h["access-control-allow-methods"]) {
		if it == "*" {
			if !include {
				anyM = true
			}
			continue
		}
		methods = append(methods, it)
	}
	for _, it := range mSplitList(h["access-control-allow-headers"]) {
		if it == "*" {
			if !include {
				anyH = true
			}
			continue
		}
		headers = append(headers, mNorm(it))
	}
	age := m.cfg.MaxAgeDefault
	if v := strings.TrimSpace(h["access-control-max-age"]); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			age = n
		}
	}
	if age > m.cfg.MaxAgeMax {
		age = m.cfg.MaxAgeMax
	}
	if !(allowOrigin == req.Origin || (allowOrigin == "*" && !include)) {
		return &Error{Code: ErrCodePreflightFailed, Reason: FailOrigin}
	}
	if include && !allowCreds {
		return &Error{Code: ErrCodePreflightFailed, Reason: FailCredentials}
	}
	_, nonSafe := m.classify(req)
	if !anyM && !mContains(methods, req.Method) {
		return &Error{Code: ErrCodePreflightFailed, Reason: FailMethod}
	}
	for _, hh := range nonSafe {
		if !anyH && !mContains(headers, hh) {
			return &Error{Code: ErrCodePreflightFailed, Reason: FailHeader}
		}
	}
	if age > 0 {
		m.store(req, methods, headers, anyM, anyH, m.now+age)
	}
	return nil
}

func (m *model) store(req Request, methods, headers []string, anyM, anyH bool, exp int64) {
	idx := -1
	for i, e := range m.entries {
		if e.origin == req.Origin && e.target == req.Target && e.creds == req.Credentials {
			idx = i
			break
		}
	}
	if idx >= 0 && m.entries[idx].exp > m.now {
		e := m.entries[idx]
		for _, mm := range methods {
			if !mContains(e.methods, mm) {
				e.methods = append(e.methods, mm)
			}
		}
		for _, hh := range headers {
			if !mContains(e.headers, hh) {
				e.headers = append(e.headers, hh)
			}
		}
		e.anyM, e.anyH = anyM, anyH
		e.exp = exp
		e.lastHit = m.now
		return
	}
	if idx >= 0 {
		m.entries = append(m.entries[:idx], m.entries[idx+1:]...)
	} else {
		kept := m.entries[:0]
		for _, e := range m.entries {
			if e.exp > m.now {
				kept = append(kept, e)
			}
		}
		m.entries = kept
		for len(m.entries) >= m.cfg.MaxEntries {
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
	m.seq++
	m.entries = append(m.entries, &mEntry{
		origin: req.Origin, target: req.Target, creds: req.Credentials,
		methods: methods, headers: headers, anyM: anyM, anyH: anyH,
		exp: exp, lastHit: m.now, created: m.seq,
	})
}

func (m *model) actual(req Request, resp ActualResponse) ([]string, *Error) {
	if err := m.validate(req); err != nil {
		return nil, err
	}
	h := map[string]string{}
	for n, v := range resp.Headers {
		h[mNorm(n)] = v
	}
	include := req.Credentials == CredentialsInclude
	allowOrigin := strings.TrimSpace(h["access-control-allow-origin"])
	if !(allowOrigin == req.Origin || (allowOrigin == "*" && !include)) {
		return nil, &Error{Code: ErrCodeResponseValidation, Reason: FailOrigin}
	}
	if include && !strings.EqualFold(strings.TrimSpace(h["access-control-allow-credentials"]), "true") {
		return nil, &Error{Code: ErrCodeResponseValidation, Reason: FailCredentials}
	}
	safe := map[string]bool{}
	for _, n := range m.cfg.SafeResponseHeaders {
		safe[mNorm(n)] = true
	}
	set := map[string]bool{}
	for name := range h {
		if safe[name] {
			set[name] = true
		}
	}
	wild := false
	for _, it := range mSplitList(h["access-control-expose-headers"]) {
		if it == "*" {
			wild = true
			continue
		}
		set[mNorm(it)] = true
	}
	if wild && !include {
		for name := range h {
			set[name] = true
		}
	}
	return sortedKeys(set), nil
}

func (m *model) redirect(req Request, newTarget string) (Request, Decision, *Error) {
	if err := m.validate(req); err != nil {
		return Request{}, Decision{}, err
	}
	if newTarget == "" {
		return Request{}, Decision{}, &Error{Code: ErrCodeInvalidArgument, Msg: "empty redirect target"}
	}
	if newTarget != req.Target {
		m.opaque++
		req.Origin = fmt.Sprintf("opaque#%d", m.opaque)
		req.Target = newTarget
	}
	d, _ := m.decide(req)
	return req, d, nil
}

func (m *model) advance(delta int64) *Error {
	if delta < 0 {
		return &Error{Code: ErrCodeClockRollback, Msg: "negative delta"}
	}
	m.now += delta
	return nil
}

func (m *model) purgeOrigin(origin string) int {
	n := 0
	kept := m.entries[:0]
	for _, e := range m.entries {
		if e.origin == origin {
			n++
		} else {
			kept = append(kept, e)
		}
	}
	m.entries = kept
	return n
}

func (m *model) purgeTarget(target string) int {
	n := 0
	kept := m.entries[:0]
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

func (m *model) live() int {
	n := 0
	for _, e := range m.entries {
		if e.exp > m.now {
			n++
		}
	}
	return n
}

// ---- 随机操作序列差分对照 ----

func errKey(err error) string {
	if err == nil {
		return "nil"
	}
	var ce *Error
	if errors.As(err, &ce) {
		if ce == nil {
			return "nil"
		}
		return ce.Code.String() + "/" + ce.Reason.String()
	}
	return "unknown:" + err.Error()
}

type gen struct {
	r    *rand.Rand
	pool []Request
}

var (
	genOrigins = []string{"https://o1.example", "https://o2.example", "https://o3.example"}
	genTargets = []string{"https://t1.example", "https://t2.example"}
	genMethods = []string{"GET", "POST", "PUT", "DELETE"}
	genNames   = []string{"accept", "content-type", "x-a", "x-b", "x-c"}
	genValues  = []string{"a", "ok", "text/plain", "text/json", "toolongvalue", "bad!"}
)

func varyCase(r *rand.Rand, s string) string {
	b := []byte(s)
	for i := range b {
		if 'a' <= b[i] && b[i] <= 'z' && r.Intn(2) == 0 {
			b[i] -= 32
		}
	}
	return string(b)
}

func (g *gen) request() Request {
	req := Request{
		Origin: genOrigins[g.r.Intn(len(genOrigins))],
		Target: genTargets[g.r.Intn(len(genTargets))],
		Method: genMethods[g.r.Intn(len(genMethods))],
	}
	if g.r.Intn(3) == 0 {
		req.Credentials = CredentialsInclude
	}
	for i, n := 0, g.r.Intn(4); i < n; i++ {
		req.Headers = append(req.Headers, Header{
			Name:  varyCase(g.r, genNames[g.r.Intn(len(genNames))]),
			Value: genValues[g.r.Intn(len(genValues))],
		})
	}
	return req
}

func (g *gen) preflightResp(req Request) PreflightResponse {
	if g.r.Intn(20) == 0 {
		return PreflightResponse{Redirect: true}
	}
	h := map[string]string{}
	switch g.r.Intn(10) {
	case 0, 1, 2, 3, 4:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = req.Origin
	case 5, 6:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = "*"
	case 7, 8:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = "https://other.example"
	}
	if g.r.Intn(2) == 0 {
		h[varyCase(g.r, "Access-Control-Allow-Credentials")] = "true"
	}
	if g.r.Intn(4) == 0 {
		h[varyCase(g.r, "Access-Control-Allow-Methods")] = "*"
	} else {
		var ms []string
		for _, m := range genMethods {
			if g.r.Intn(2) == 0 {
				ms = append(ms, m)
			}
		}
		if g.r.Intn(2) == 0 {
			ms = append(ms, req.Method)
		}
		h[varyCase(g.r, "Access-Control-Allow-Methods")] = joinComma(ms)
	}
	if g.r.Intn(4) == 0 {
		h[varyCase(g.r, "Access-Control-Allow-Headers")] = "*"
	} else {
		var hs []string
		for _, n := range []string{"x-a", "x-b", "x-c"} {
			if g.r.Intn(2) == 0 {
				hs = append(hs, varyCase(g.r, n))
			}
		}
		for _, rh := range req.Headers {
			if g.r.Intn(2) == 0 {
				hs = append(hs, varyCase(g.r, rh.Name))
			}
		}
		h[varyCase(g.r, "Access-Control-Allow-Headers")] = joinComma(hs)
	}
	switch g.r.Intn(7) {
	case 1:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "0"
	case 2:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "-2"
	case 3:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "1"
	case 4:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "3"
	case 5:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "9"
	case 6:
		h[varyCase(g.r, "Access-Control-Max-Age")] = "6"
	}
	return PreflightResponse{Headers: h}
}

func joinComma(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}

func (g *gen) actualResp(req Request) ActualResponse {
	h := map[string]string{}
	switch g.r.Intn(10) {
	case 0, 1, 2, 3, 4:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = req.Origin
	case 5, 6:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = "*"
	case 7:
		h[varyCase(g.r, "Access-Control-Allow-Origin")] = "https://other.example"
	}
	if g.r.Intn(2) == 0 {
		h[varyCase(g.r, "Access-Control-Allow-Credentials")] = "true"
	}
	switch g.r.Intn(4) {
	case 1:
		h[varyCase(g.r, "Access-Control-Expose-Headers")] = "*"
	case 2, 3:
		var es []string
		for _, n := range []string{"x-a", "x-b", "cache-control"} {
			if g.r.Intn(2) == 0 {
				es = append(es, varyCase(g.r, n))
			}
		}
		h[varyCase(g.r, "Access-Control-Expose-Headers")] = joinComma(es)
	}
	if g.r.Intn(2) == 0 {
		h[varyCase(g.r, "Cache-Control")] = "no-cache"
	}
	if g.r.Intn(3) == 0 {
		h[varyCase(g.r, "X-A")] = "1"
	}
	return ActualResponse{Headers: h}
}

func modelConfig() Config {
	return Config{
		SafeMethods: []string{"GET", "POST"},
		SafeHeaders: map[string]HeaderRule{
			"Accept": {MaxLen: 8},
			"Content-Type": {MaxLen: 10, Allowed: func(b byte) bool {
				return ('a' <= b && b <= 'z') || b == '/'
			}},
		},
		SafeResponseHeaders: []string{"Cache-Control"},
		MaxEntries:          3,
		MaxAgeDefault:       2,
		MaxAgeMax:           6,
	}
}

func diffDecision(a, b Decision) bool {
	return a.Action == b.Action && a.CacheHit == b.CacheHit && a.Simple == b.Simple &&
		reflect.DeepEqual(a.NonSafeHeaders, b.NonSafeHeaders)
}

func TestModelDifferential(t *testing.T) {
	for seed := int64(1); seed <= 3; seed++ {
		t.Run(genOrigins[0]+"/seed", func(t *testing.T) { runDiff(t, seed) })
	}
}

func runDiff(t *testing.T, seed int64) {
	cfg := modelConfig()
	k, err := NewKernel(cfg)
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	m := newModel(cfg)
	g := &gen{r: rand.New(rand.NewSource(seed))}

	checkState := func(op int, what string) {
		t.Helper()
		if k.Now() != m.now {
			t.Fatalf("op %d (%s): clock diverged: kernel=%d model=%d", op, what, k.Now(), m.now)
		}
		if k.CacheSize() != len(m.entries) {
			t.Fatalf("op %d (%s): cache size diverged: kernel=%d model=%d", op, what, k.CacheSize(), len(m.entries))
		}
		if k.LiveEntries() != m.live() {
			t.Fatalf("op %d (%s): live entries diverged: kernel=%d model=%d", op, what, k.LiveEntries(), m.live())
		}
		if k.CacheSize() > cfg.MaxEntries {
			t.Fatalf("op %d (%s): capacity invariant violated: %d > %d", op, what, k.CacheSize(), cfg.MaxEntries)
		}
	}

	pick := func() Request {
		if len(g.pool) == 0 {
			req := g.request()
			g.pool = append(g.pool, req)
			return req
		}
		return g.pool[g.r.Intn(len(g.pool))]
	}

	for op := 0; op < 4000; op++ {
		switch n := g.r.Intn(100); {
		case n < 30: // 新请求判定
			req := g.request()
			if len(g.pool) < 8 {
				g.pool = append(g.pool, req)
			} else {
				g.pool[g.r.Intn(len(g.pool))] = req
			}
			dk, ek := k.Decide(req)
			dm, em := m.decide(req)
			if errKey(ek) != errKey(em) {
				t.Fatalf("op %d decide: err kernel=%s model=%s req=%+v", op, errKey(ek), errKey(em), req)
			}
			if ek == nil && !diffDecision(dk, dm) {
				t.Fatalf("op %d decide: kernel=%+v model=%+v req=%+v", op, dk, dm, req)
			}
			checkState(op, "decide")
		case n < 45: // 池内请求再判定
			req := pick()
			dk, ek := k.Decide(req)
			dm, em := m.decide(req)
			if errKey(ek) != errKey(em) || (ek == nil && !diffDecision(dk, dm)) {
				t.Fatalf("op %d re-decide: kernel=(%+v,%s) model=(%+v,%s) req=%+v",
					op, dk, errKey(ek), dm, errKey(em), req)
			}
			checkState(op, "re-decide")
		case n < 65: // 预检响应
			req := pick()
			resp := g.preflightResp(req)
			ek := k.SubmitPreflightResponse(req, resp)
			em := m.preflight(req, resp)
			if errKey(ek) != errKey(em) {
				t.Fatalf("op %d preflight: kernel=%s model=%s req=%+v resp=%+v",
					op, errKey(ek), errKey(em), req, resp)
			}
			checkState(op, "preflight")
		case n < 80: // 实际响应
			req := pick()
			resp := g.actualResp(req)
			xk, ek := k.SubmitActualResponse(req, resp)
			xm, em := m.actual(req, resp)
			if errKey(ek) != errKey(em) {
				t.Fatalf("op %d actual: kernel=%s model=%s req=%+v resp=%+v",
					op, errKey(ek), errKey(em), req, resp)
			}
			if ek == nil && !reflect.DeepEqual(xk, xm) {
				t.Fatalf("op %d actual: exposed kernel=%v model=%v req=%+v resp=%+v", op, xk, xm, req, resp)
			}
			checkState(op, "actual")
		case n < 90: // 重定向
			if len(g.pool) == 0 {
				g.pool = append(g.pool, g.request())
			}
			idx := g.r.Intn(len(g.pool))
			req := g.pool[idx]
			target := genTargets[g.r.Intn(len(genTargets))]
			rk, dk, ek := k.SubmitRedirect(req, target)
			rm, dm, em := m.redirect(req, target)
			if errKey(ek) != errKey(em) {
				t.Fatalf("op %d redirect: kernel=%s model=%s", op, errKey(ek), errKey(em))
			}
			if ek == nil {
				if !reflect.DeepEqual(rk, rm) || !diffDecision(dk, dm) {
					t.Fatalf("op %d redirect: kernel=(%+v,%+v) model=(%+v,%+v)", op, rk, dk, rm, dm)
				}
				g.pool[idx] = rk
			}
			checkState(op, "redirect")
		case n < 95: // 时钟推进（含回退）
			delta := int64(g.r.Intn(9) - 2)
			ek := k.Advance(delta)
			em := m.advance(delta)
			if errKey(ek) != errKey(em) {
				t.Fatalf("op %d advance(%d): kernel=%s model=%s", op, delta, errKey(ek), errKey(em))
			}
			checkState(op, "advance")
		case n < 98: // 按来源清除
			o := genOrigins[g.r.Intn(len(genOrigins))]
			if k.PurgeByOrigin(o) != m.purgeOrigin(o) {
				t.Fatalf("op %d purgeOrigin(%s) diverged", op, o)
			}
			checkState(op, "purgeOrigin")
		default: // 按目标清除
			tg := genTargets[g.r.Intn(len(genTargets))]
			if k.PurgeByTarget(tg) != m.purgeTarget(tg) {
				t.Fatalf("op %d purgeTarget(%s) diverged", op, tg)
			}
			checkState(op, "purgeTarget")
		}
	}
}
