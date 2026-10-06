package cors

import (
	"fmt"
	"sort"
	"sync"
)

// Engine 是预检判定与缓存内核。所有公开方法都可并发调用，
// 内部以单互斥锁串行化，结果等价于某个串行顺序。
type Engine struct {
	mu     sync.Mutex
	cfg    Config
	class  *classifier
	cache  *cache
	clock  Clock
	opaque uint64
	logger func(string)
}

// NewEngine 校验配置并构造内核。
func NewEngine(cfg Config) (*Engine, *Error) {
	if cfg.Capacity <= 0 {
		return nil, invalidArg("cache capacity must be positive, got %d", cfg.Capacity)
	}
	if cfg.MaxMaxAge <= 0 {
		return nil, invalidArg("max max-age must be positive, got %d", cfg.MaxMaxAge)
	}
	if cfg.DefaultMaxAge < 0 {
		return nil, invalidArg("default max-age must not be negative, got %d", cfg.DefaultMaxAge)
	}
	class, err := newClassifier(cfg)
	if err != nil {
		return nil, err
	}
	return &Engine{cfg: cfg, class: class, cache: newCache(cfg.Capacity)}, nil
}

// SetLogger 设置判定日志钩子，入参为单行日志（输入、输出与判定依据）。
func (e *Engine) SetLogger(fn func(string)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger = fn
}

func (e *Engine) logf(format string, args ...any) {
	if e.logger != nil {
		e.logger(fmt.Sprintf(format, args...))
	}
}

// Now 返回内核时钟当前时刻。
func (e *Engine) Now() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock.Now()
}

// AdvanceClock 推进时钟；负增量视为时钟回退，拒绝且时钟不变。
func (e *Engine) AdvanceClock(delta int64) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if delta < 0 {
		err := &Error{Kind: ErrKindClockRollback, Message: fmt.Sprintf("negative delta %d", delta)}
		e.logf("ADVANCE delta=%d => rejected: %s", delta, err)
		return err
	}
	e.clock.now += delta
	e.logf("ADVANCE delta=%d => now=%d", delta, e.clock.now)
	return nil
}

// validateRequest 校验请求参数（空来源、空方法、非法头名字等）。
func (e *Engine) validateRequest(req Request) *Error {
	if !req.Origin.opaque && req.Origin.value == "" {
		return invalidArg("empty origin")
	}
	if req.Target == "" {
		return invalidArg("empty target")
	}
	if req.Method == "" {
		return invalidArg("empty method")
	}
	for _, h := range req.Headers {
		if _, ok := normalizeHeaderName(h.Name); !ok {
			return invalidArg("invalid header name %q", h.Name)
		}
	}
	return nil
}

func describeRequest(req Request) string {
	return fmt.Sprintf("origin=%s target=%q method=%q headers=%v creds=%v",
		req.Origin, req.Target, req.Method, req.Headers, req.IncludeCredentials)
}

func (e *Engine) cacheKeyOf(req Request) cacheKey {
	return cacheKey{origin: req.Origin.cacheKey(), target: req.Target, credentials: req.IncludeCredentials}
}

// Decide 对请求进行分类与缓存查询，返回判定结果。
func (e *Engine) Decide(req Request) (Decision, *Error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateRequest(req); err != nil {
		e.logf("DECIDE %s => rejected: %s", describeRequest(req), err)
		return Decision{}, err
	}
	if req.Origin.Equal(NewOrigin(req.Target)) {
		d := Decision{Kind: DecisionSameOrigin, Detail: "origin equals target, kernel not involved"}
		e.logf("DECIDE %s => %s (%s)", describeRequest(req), d.Kind, d.Detail)
		return d, nil
	}
	if e.class.isSimple(req) {
		d := Decision{Kind: DecisionSimple, Detail: "safe method and every header safe with constrained value"}
		e.logf("DECIDE %s => %s (%s)", describeRequest(req), d.Kind, d.Detail)
		return d, nil
	}
	nonSafe := e.class.nonSafeHeaders(req)
	now := e.clock.Now()
	key := e.cacheKeyOf(req)
	if ent := e.cache.entries[key]; ent != nil {
		if now < ent.expiry && ent.allows(req.Method, nonSafe) {
			ent.lastHit = now
			d := Decision{Kind: DecisionCacheHit,
				Detail: fmt.Sprintf("cache entry covers method and non-safe headers %v until %d", nonSafe, ent.expiry)}
			e.logf("DECIDE %s => %s (%s)", describeRequest(req), d.Kind, d.Detail)
			return d, nil
		}
	}
	d := Decision{Kind: DecisionPreflightNeeded,
		Detail: fmt.Sprintf("no live cache entry covers method %q and non-safe headers %v", req.Method, nonSafe)}
	e.logf("DECIDE %s => %s (%s)", describeRequest(req), d.Kind, d.Detail)
	return d, nil
}

// originAllowed 校验响应中的允许来源：等于发起来源或为通配；
// 凭据模式为包含时通配无效；不透明来源不等于任何字面值。
func originAllowed(req Request, allowOrigin string) bool {
	if allowOrigin == "*" {
		return !req.IncludeCredentials
	}
	if req.Origin.opaque {
		return false
	}
	return allowOrigin == req.Origin.value
}

func normalizeNameList(names []string) ([]string, *Error) {
	out := make([]string, 0, len(names))
	for _, n := range names {
		nn, ok := normalizeHeaderName(n)
		if !ok {
			return nil, invalidArg("invalid header name %q in response", n)
		}
		out = append(out, nn)
	}
	return out, nil
}

// effectiveMaxAge 由响应声明推出存活时长；返回 false 表示不缓存。
func (e *Engine) effectiveMaxAge(declared *int64) (int64, bool) {
	age := e.cfg.DefaultMaxAge
	if declared != nil {
		if *declared <= 0 {
			return 0, false
		}
		age = *declared
	}
	if age > e.cfg.MaxMaxAge {
		age = e.cfg.MaxMaxAge
	}
	if age <= 0 {
		return 0, false
	}
	return age, true
}

// SubmitPreflight 校验预检响应；成功时按规则写入或合并缓存条目。
func (e *Engine) SubmitPreflight(req Request, resp PreflightResponse) *Error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateRequest(req); err != nil {
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	allowHeaders, err := normalizeNameList(resp.AllowHeaders)
	if err != nil {
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	if resp.Redirected {
		err := &Error{Kind: ErrKindRedirectNotAllowed, Message: "preflight must not be redirected"}
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	if !originAllowed(req, resp.AllowOrigin) {
		err := preflightErr(ReasonOriginMismatch, "allow-origin %q does not match origin", resp.AllowOrigin)
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	if req.IncludeCredentials && !resp.AllowCredentials {
		err := preflightErr(ReasonCredentialsMismatch, "credentials included but not allowed by response")
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	anyMethod := resp.AllowAnyMethod && !req.IncludeCredentials
	if !anyMethod && !containsString(resp.AllowMethods, req.Method) {
		err := preflightErr(ReasonMethodMismatch, "method %q not in allow-methods %v", req.Method, resp.AllowMethods)
		e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
		return err
	}
	anyHeader := resp.AllowAnyHeader && !req.IncludeCredentials
	nonSafe := e.class.nonSafeHeaders(req)
	if !anyHeader {
		allowed := make(map[string]struct{}, len(allowHeaders))
		for _, h := range allowHeaders {
			allowed[h] = struct{}{}
		}
		for _, h := range nonSafe {
			if _, ok := allowed[h]; !ok {
				err := preflightErr(ReasonHeaderMismatch, "header %q not in allow-headers %v", h, allowHeaders)
				e.logf("PREFLIGHT %s => rejected: %s", describeRequest(req), err)
				return err
			}
		}
	}
	age, cacheable := e.effectiveMaxAge(resp.MaxAge)
	if !cacheable {
		e.logf("PREFLIGHT %s => ok, not cached (non-positive max-age)", describeRequest(req))
		return nil
	}
	now := e.clock.Now()
	expiry := now + age
	key := e.cacheKeyOf(req)
	if ent := e.cache.entries[key]; ent != nil && now < ent.expiry {
		for _, m := range resp.AllowMethods {
			ent.methods[m] = struct{}{}
		}
		for _, h := range allowHeaders {
			ent.headers[h] = struct{}{}
		}
		ent.anyMethod = anyMethod
		ent.anyHeader = anyHeader
		ent.expiry = expiry
		e.logf("PREFLIGHT %s => ok, merged into entry: methods+=%v headers+=%v expiry=%d",
			describeRequest(req), resp.AllowMethods, allowHeaders, expiry)
		return nil
	}
	e.cache.seq++
	ent := &cacheEntry{
		methods:    make(map[string]struct{}, len(resp.AllowMethods)),
		headers:    make(map[string]struct{}, len(allowHeaders)),
		anyMethod:  anyMethod,
		anyHeader:  anyHeader,
		expiry:     expiry,
		lastHit:    now,
		createdSeq: e.cache.seq,
	}
	for _, m := range resp.AllowMethods {
		ent.methods[m] = struct{}{}
	}
	for _, h := range allowHeaders {
		ent.headers[h] = struct{}{}
	}
	e.cache.put(key, ent, now)
	e.logf("PREFLIGHT %s => ok, new entry: methods=%v headers=%v anyMethod=%v anyHeader=%v expiry=%d",
		describeRequest(req), resp.AllowMethods, allowHeaders, anyMethod, anyHeader, expiry)
	return nil
}

// SubmitActual 校验实际响应；返回暴露给调用方的头或重定向后续请求。
func (e *Engine) SubmitActual(req Request, resp ActualResponse) (ActualResult, *Error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateRequest(req); err != nil {
		e.logf("ACTUAL %s => rejected: %s", describeRequest(req), err)
		return ActualResult{}, err
	}
	exposeNames, err := normalizeNameList(resp.ExposeHeaders)
	if err != nil {
		e.logf("ACTUAL %s => rejected: %s", describeRequest(req), err)
		return ActualResult{}, err
	}
	if resp.RedirectTo != "" {
		followUp := req
		followUp.Target = resp.RedirectTo
		if resp.RedirectTo != req.Target {
			e.opaque++
			followUp.Origin = opaqueOrigin(e.opaque)
		}
		e.logf("ACTUAL %s => redirect to %q, follow-up origin=%s", describeRequest(req), resp.RedirectTo, followUp.Origin)
		return ActualResult{Redirect: true, FollowUp: followUp}, nil
	}
	if !originAllowed(req, resp.AllowOrigin) {
		err := &Error{Kind: ErrKindResponseValidationFailed, Reason: ReasonOriginMismatch,
			Message: fmt.Sprintf("allow-origin %q does not match origin", resp.AllowOrigin)}
		e.logf("ACTUAL %s => rejected: %s", describeRequest(req), err)
		return ActualResult{}, err
	}
	if req.IncludeCredentials && !resp.AllowCredentials {
		err := &Error{Kind: ErrKindResponseValidationFailed, Reason: ReasonCredentialsMismatch,
			Message: "credentials included but not allowed by response"}
		e.logf("ACTUAL %s => rejected: %s", describeRequest(req), err)
		return ActualResult{}, err
	}
	wildcard := resp.ExposeAnyHeader && !req.IncludeCredentials
	exposedSet := make(map[string]struct{}, len(e.class.safeResponses)+len(exposeNames))
	for n := range e.class.safeResponses {
		exposedSet[n] = struct{}{}
	}
	for _, n := range exposeNames {
		exposedSet[n] = struct{}{}
	}
	var out []Header
	for _, h := range resp.Headers {
		nn, ok := normalizeHeaderName(h.Name)
		if !ok {
			continue
		}
		if _, ok := exposedSet[nn]; ok || wildcard {
			out = append(out, h)
		}
	}
	e.logf("ACTUAL %s => ok, exposed=%d headers (wildcard=%v)", describeRequest(req), len(out), wildcard)
	return ActualResult{Exposed: out}, nil
}

// PurgeByOrigin 清除指定发起来源的所有缓存条目，返回清除数。
func (e *Engine) PurgeByOrigin(origin Origin) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.cache.purgeByOrigin(origin.cacheKey())
	e.logf("PURGE origin=%s => removed=%d", origin, n)
	return n
}

// PurgeByTarget 清除指定目标来源的所有缓存条目，返回清除数。
func (e *Engine) PurgeByTarget(target string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := e.cache.purgeByTarget(target)
	e.logf("PURGE target=%q => removed=%d", target, n)
	return n
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// EntrySnapshot 是缓存条目的可比较快照，用于测试与审计。
type EntrySnapshot struct {
	Origin      string
	Target      string
	Credentials bool
	Methods     []string
	Headers     []string
	AnyMethod   bool
	AnyHeader   bool
	Expiry      int64
}

// Snapshot 返回全部缓存条目的有序快照。
func (e *Engine) Snapshot() []EntrySnapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]EntrySnapshot, 0, len(e.cache.entries))
	for k, ent := range e.cache.entries {
		snap := EntrySnapshot{
			Origin:      k.origin,
			Target:      k.target,
			Credentials: k.credentials,
			AnyMethod:   ent.anyMethod,
			AnyHeader:   ent.anyHeader,
			Expiry:      ent.expiry,
		}
		for m := range ent.methods {
			snap.Methods = append(snap.Methods, m)
		}
		for h := range ent.headers {
			snap.Headers = append(snap.Headers, h)
		}
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

// EntryCount 返回当前缓存条目数。
func (e *Engine) EntryCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.cache.entries)
}
