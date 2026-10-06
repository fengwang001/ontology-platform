package cors

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Kernel 是预检判定与预检结果缓存内核，并发安全。
type Kernel struct {
	mu          sync.Mutex
	safeMethods map[string]struct{}
	safeHeaders map[string]HeaderRule
	safeResp    map[string]struct{}
	maxAgeDef   int64
	maxAgeMax   int64
	logf        func(format string, args ...any)

	now    int64 // 逻辑时钟，只能前进
	opaque uint64
	cache  *cache
}

// NewKernel 校验配置并构造内核；上限非正时报参数非法。
func NewKernel(cfg Config) (*Kernel, error) {
	if cfg.MaxEntries <= 0 {
		return nil, invalidArg("MaxEntries must be positive")
	}
	if cfg.MaxAgeMax <= 0 {
		return nil, invalidArg("MaxAgeMax must be positive")
	}
	if cfg.MaxAgeDefault < 0 {
		return nil, invalidArg("MaxAgeDefault must be non-negative")
	}
	k := &Kernel{
		safeMethods: make(map[string]struct{}, len(cfg.SafeMethods)),
		safeHeaders: make(map[string]HeaderRule, len(cfg.SafeHeaders)),
		safeResp:    make(map[string]struct{}, len(cfg.SafeResponseHeaders)),
		maxAgeDef:   cfg.MaxAgeDefault,
		maxAgeMax:   cfg.MaxAgeMax,
		logf:        cfg.Logf,
		cache:       newCache(cfg.MaxEntries),
	}
	for _, m := range cfg.SafeMethods {
		if m == "" {
			return nil, invalidArg("empty safe method")
		}
		k.safeMethods[m] = struct{}{}
	}
	for name, rule := range cfg.SafeHeaders {
		if !validHeaderName(name) {
			return nil, invalidArg("illegal safe header name: " + name)
		}
		if rule.MaxLen <= 0 {
			return nil, invalidArg("safe header MaxLen must be positive: " + name)
		}
		k.safeHeaders[normalizeHeaderName(name)] = rule
	}
	for _, name := range cfg.SafeResponseHeaders {
		if !validHeaderName(name) {
			return nil, invalidArg("illegal safe response header name: " + name)
		}
		k.safeResp[normalizeHeaderName(name)] = struct{}{}
	}
	return k, nil
}

func (k *Kernel) log(format string, args ...any) {
	if k.logf != nil {
		k.logf("cors: "+format, args...)
	}
}

// Now 返回当前逻辑时钟。
func (k *Kernel) Now() int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.now
}

// Advance 推进逻辑时钟；负增量为时钟回退，拒绝且时钟不变。
func (k *Kernel) Advance(delta int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if delta < 0 {
		k.log("advance(%d) rejected: clock rollback, now=%d", delta, k.now)
		return &Error{Code: ErrCodeClockRollback, Msg: fmt.Sprintf("negative delta %d", delta)}
	}
	k.now += delta
	k.log("advance(%d) ok: now=%d", delta, k.now)
	return nil
}

// CacheSize 返回缓存条目总数（含已过期但尚未清除的），用于核对容量不变量。
func (k *Kernel) CacheSize() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.cache.m)
}

// LiveEntries 返回未过期条目数，用于测试核对。
func (k *Kernel) LiveEntries() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cache.live(k.now)
}

// PurgeByOrigin 按发起来源清除条目，返回清除数。
func (k *Kernel) PurgeByOrigin(origin string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := k.cache.purgeByOrigin(origin)
	k.log("purge origin=%q removed=%d", origin, n)
	return n
}

// PurgeByTarget 按目标来源清除条目，返回清除数。
func (k *Kernel) PurgeByTarget(target string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := k.cache.purgeByTarget(target)
	k.log("purge target=%q removed=%d", target, n)
	return n
}

// Decide 判定请求：简单请求直接发出；需预检请求先查缓存，命中且覆盖
// 本次方法与全部非安全头则跳过预检，否则要求发起（新的）预检。
func (k *Kernel) Decide(req Request) (Decision, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := validateRequest(req); err != nil {
		k.log("decide %+v rejected: %v", req, err)
		return Decision{}, err
	}
	d := k.decideLocked(req)
	k.log("decide origin=%q target=%q method=%q creds=%s => %s (hit=%v simple=%v): %s",
		req.Origin, req.Target, req.Method, req.Credentials, d.Action, d.CacheHit, d.Simple, d.Reason)
	return d, nil
}

func (k *Kernel) decideLocked(req Request) Decision {
	simple, nonSafe := k.classify(req)
	if simple {
		return Decision{Action: ActionSend, Simple: true, Reason: "simple request"}
	}
	key := cacheKey{origin: req.Origin, target: req.Target, creds: req.Credentials}
	if e := k.cache.get(key, k.now); e != nil {
		if e.covers(req.Method, nonSafe) {
			e.lastHit = k.now
			return Decision{Action: ActionSend, CacheHit: true, NonSafeHeaders: nonSafe,
				Reason: "cache hit: method and all non-safe headers allowed"}
		}
		return Decision{Action: ActionPreflight, NonSafeHeaders: nonSafe,
			Reason: "cache entry does not cover method/headers, re-preflight"}
	}
	return Decision{Action: ActionPreflight, NonSafeHeaders: nonSafe, Reason: "no usable cache entry"}
}

// SubmitPreflightResponse 校验并采纳预检响应。
// 拒绝次序：参数非法 > 重定向不允许 > 预检失败（来源>凭据>方法>头）。
// 预检失败不改动已有缓存条目；成功且可缓存时写入或与旧条目合并。
func (k *Kernel) SubmitPreflightResponse(req Request, resp PreflightResponse) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := validateRequest(req); err != nil {
		k.log("preflight-response %+v rejected: %v", req, err)
		return err
	}
	if resp.Redirect {
		k.log("preflight-response origin=%q target=%q rejected: redirect not allowed", req.Origin, req.Target)
		return &Error{Code: ErrCodeRedirectNotAllowed, Msg: "preflight redirected"}
	}
	h := normalizeHeaderMap(resp.Headers)
	p := parsePreflight(h, req.Credentials == CredentialsInclude, k.now, k.maxAgeDef, k.maxAgeMax)
	_, nonSafe := k.classify(req)
	if reason := checkPreflight(p, req, nonSafe); reason != FailNone {
		err := &Error{Code: ErrCodePreflightFailed, Reason: reason,
			Msg: fmt.Sprintf("origin=%q target=%q method=%q", req.Origin, req.Target, req.Method)}
		k.log("preflight-response rejected: %v (cache untouched)", err)
		return err
	}
	if p.cacheable {
		key := cacheKey{origin: req.Origin, target: req.Target, creds: req.Credentials}
		k.cache.store(key, p, k.now)
		k.log("preflight-response accepted: cached until=%d anyMethod=%v anyHeader=%v methods=%v headers=%v",
			p.expiresAt, p.anyMethod, p.anyHeader, sortedKeys(p.methods), sortedKeys(p.headers))
	} else {
		k.log("preflight-response accepted: max-age<=0, result applies to this request only (cache untouched)")
	}
	return nil
}

// SubmitActualResponse 校验实际响应并计算暴露给调用方的头集合。
// 校验失败不影响缓存条目。
func (k *Kernel) SubmitActualResponse(req Request, resp ActualResponse) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := validateRequest(req); err != nil {
		k.log("actual-response %+v rejected: %v", req, err)
		return nil, err
	}
	h := normalizeHeaderMap(resp.Headers)
	include := req.Credentials == CredentialsInclude
	allowOrigin := strings.TrimSpace(h["access-control-allow-origin"])
	if !originAllowed(allowOrigin, req.Origin, include) {
		err := &Error{Code: ErrCodeResponseValidation, Reason: FailOrigin,
			Msg: fmt.Sprintf("allow-origin %q vs origin %q", allowOrigin, req.Origin)}
		k.log("actual-response rejected: %v (cache untouched)", err)
		return nil, err
	}
	if include && !credsAllowed(h) {
		err := &Error{Code: ErrCodeResponseValidation, Reason: FailCredentials,
			Msg: "credentials included but not allowed by response"}
		k.log("actual-response rejected: %v (cache untouched)", err)
		return nil, err
	}
	exposed := k.exposedHeaders(h, include)
	k.log("actual-response accepted: exposed=%v", exposed)
	return exposed, nil
}

// SubmitRedirect 处理实际请求的重定向。重定向到另一来源时，后续请求的
// 发起来源变为不透明来源（唯一生成，与任何目标都不相等）；若请求本来需
// 预检，则对新目标重新判定（以不透明来源为键）。返回后续请求与判定。
func (k *Kernel) SubmitRedirect(req Request, newTarget string) (Request, Decision, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := validateRequest(req); err != nil {
		k.log("redirect %+v rejected: %v", req, err)
		return Request{}, Decision{}, err
	}
	if newTarget == "" {
		return Request{}, Decision{}, invalidArg("empty redirect target")
	}
	if newTarget != req.Target {
		k.opaque++
		req.Origin = fmt.Sprintf("opaque#%d", k.opaque)
		req.Target = newTarget
	}
	d := k.decideLocked(req)
	k.log("redirect => origin=%q target=%q decision=%s: %s", req.Origin, req.Target, d.Action, d.Reason)
	return req, d, nil
}

// originAllowed 实现允许来源规则：等于发起来源，或为通配（凭据包含时通配无效）。
func originAllowed(allowOrigin, origin string, include bool) bool {
	if allowOrigin == "*" {
		return !include
	}
	return allowOrigin == origin
}

func credsAllowed(h map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(h["access-control-allow-credentials"]), "true")
}

// checkPreflight 按 来源>凭据>方法>头 的次序检查，返回第一个不满足的子原因。
func checkPreflight(p parsed, req Request, nonSafe []string) FailReason {
	include := req.Credentials == CredentialsInclude
	if !originAllowed(p.allowOrigin, req.Origin, include) {
		return FailOrigin
	}
	if include && !p.allowCreds {
		return FailCredentials
	}
	if !p.anyMethod && !p.methods[req.Method] {
		return FailMethod
	}
	for _, name := range nonSafe {
		if !p.anyHeader && !p.headers[name] {
			return FailHeader
		}
	}
	return FailNone
}

// exposedHeaders 计算暴露给调用方的头：安全响应头集合（取响应中实际出现的）
// 加上响应显式暴露的头；通配暴露全部响应头，但凭据包含时通配无效。
func (k *Kernel) exposedHeaders(h map[string]string, include bool) []string {
	set := make(map[string]bool)
	for name := range h {
		if _, ok := k.safeResp[name]; ok {
			set[name] = true
		}
	}
	wild := false
	for _, item := range splitList(h["access-control-expose-headers"]) {
		if item == "*" {
			wild = true
			continue
		}
		set[normalizeHeaderName(item)] = true
	}
	if wild && !include {
		for name := range h {
			set[name] = true
		}
	}
	return sortedKeys(set)
}

// parsePreflight 解析预检响应头。凭据包含时任意方法/任意头通配不生效，
// 按逐项列出处理（"*" 本身不是方法或头，直接丢弃）。
func parsePreflight(h map[string]string, include bool, now, defAge, maxAge int64) parsed {
	p := parsed{
		methods:     make(map[string]bool),
		headers:     make(map[string]bool),
		allowOrigin: strings.TrimSpace(h["access-control-allow-origin"]),
		allowCreds:  credsAllowed(h),
	}
	for _, m := range splitList(h["access-control-allow-methods"]) {
		if m == "*" {
			if !include {
				p.anyMethod = true
			}
			continue
		}
		p.methods[m] = true
	}
	for _, name := range splitList(h["access-control-allow-headers"]) {
		if name == "*" {
			if !include {
				p.anyHeader = true
			}
			continue
		}
		p.headers[normalizeHeaderName(name)] = true
	}
	age, declared := parseMaxAge(h["access-control-max-age"])
	if !declared {
		age = defAge
	}
	if age > maxAge {
		age = maxAge
	}
	p.cacheable = age > 0
	p.expiresAt = now + age
	return p
}

// parseMaxAge 解析存活时长；未声明或无法解析时按未声明处理。
func parseMaxAge(v string) (int64, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func normalizeHeaderMap(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for name, v := range h {
		out[normalizeHeaderName(name)] = v
	}
	return out
}

func splitList(v string) []string {
	var out []string
	for _, item := range strings.Split(v, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
