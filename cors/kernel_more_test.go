package cors

import "testing"

// 凭据包含时通配无效之一：允许来源为 "*" 无效。
func TestIncludeWildcardOriginInvalid(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	req.Credentials = CredentialsInclude
	resp := allowPreflight("*", "PUT", "x-token", "10")
	resp.Headers["Access-Control-Allow-Credentials"] = "true"
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailOrigin)
}

// 凭据包含时通配无效之二：任意方法、任意头的 "*" 声明不生效，按逐项列出处理。
func TestIncludeWildcardMethodAndHeaderInvalid(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	req.Credentials = CredentialsInclude
	resp := allowPreflight("https://a.example", "*", "*", "10")
	resp.Headers["Access-Control-Allow-Credentials"] = "true"
	// "*" 方法通配不生效，逐项集合为空 → 方法不符。
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailMethod)
}

// 凭据包含时通配无效之三：暴露通配 "*" 无效。
func TestIncludeWildcardExposeInvalid(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := Request{Origin: "https://a.example", Target: "https://b.example", Method: "GET",
		Credentials: CredentialsInclude}
	resp := ActualResponse{Headers: map[string]string{
		"Access-Control-Allow-Origin":      "https://a.example",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Expose-Headers":    "*",
		"Cache-Control":                    "no-cache",
		"X-Secret":                         "1",
	}}
	exposed, err := k.SubmitActualResponse(req, resp)
	if err != nil {
		t.Fatalf("actual response: %v", err)
	}
	for _, h := range exposed {
		if h == "x-secret" {
			t.Fatalf("include + wildcard expose must not expose x-secret: %v", exposed)
		}
	}
	found := false
	for _, h := range exposed {
		if h == "cache-control" {
			found = true
		}
	}
	if !found {
		t.Fatalf("safe response header cache-control must be exposed: %v", exposed)
	}

	// 对照：不带凭据时通配暴露全部响应头。
	req.Credentials = CredentialsOmit
	exposed, err = k.SubmitActualResponse(req, resp)
	if err != nil {
		t.Fatalf("actual response (omit): %v", err)
	}
	found = false
	for _, h := range exposed {
		if h == "x-secret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("omit + wildcard expose must expose x-secret: %v", exposed)
	}
}

// 缓存部分覆盖触发新预检，成功后与旧条目合并：
// 允许集合取并集，过期时刻取新值，通配标记取新响应的值。
func TestCachePartialCoverageMerge(t *testing.T) {
	k := newKernel(t, baseConfig())
	o, target := "https://a.example", "https://b.example"
	req1 := Request{Origin: o, Target: target, Method: "PUT", Headers: []Header{{"X-A", "1"}}}
	if err := k.SubmitPreflightResponse(req1, allowPreflight(o, "*", "x-a", "10")); err != nil {
		t.Fatalf("preflight 1: %v", err)
	}

	// x-b 未被旧条目覆盖 → 必须发起新预检。
	req2 := Request{Origin: o, Target: target, Method: "PUT", Headers: []Header{{"X-B", "1"}}}
	if d := mustDecide(t, k, req2); d.Action != ActionPreflight || d.CacheHit {
		t.Fatalf("partial coverage must trigger new preflight, got %+v", d)
	}
	if err := k.SubmitPreflightResponse(req2, allowPreflight(o, "PUT", "x-b", "4")); err != nil {
		t.Fatalf("preflight 2: %v", err)
	}

	// 合并后：x-a 与 x-b 都允许（并集）。
	both := Request{Origin: o, Target: target, Method: "PUT",
		Headers: []Header{{"X-A", "1"}, {"X-B", "2"}}}
	if d := mustDecide(t, k, both); !d.CacheHit {
		t.Fatalf("merged entry must cover x-a and x-b, got %+v", d)
	}
	// 通配标记取新响应的值：旧响应的任意方法通配不再生效。
	del := Request{Origin: o, Target: target, Method: "DELETE", Headers: []Header{{"X-A", "1"}}}
	if d := mustDecide(t, k, del); d.Action != ActionPreflight {
		t.Fatalf("wildcard flag must come from new response, got %+v", d)
	}
	// 过期时刻取新值 now+4。
	if err := k.Advance(4); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k, both); d.CacheHit {
		t.Fatalf("merged entry must expire at new max-age, got %+v", d)
	}
}

// 预检失败不改动已有条目。
func TestPreflightFailureKeepsEntry(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	if err := k.SubmitPreflightResponse(req, allowPreflight("https://a.example", "PUT", "x-token", "10")); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	sizeBefore := k.CacheSize()

	del := Request{Origin: req.Origin, Target: req.Target, Method: "DELETE", Headers: req.Headers}
	resp := allowPreflight("https://a.example", "GET", "x-token", "10")
	checkErr(t, k.SubmitPreflightResponse(del, resp), ErrCodePreflightFailed, FailMethod)

	if n := k.CacheSize(); n != sizeBefore {
		t.Fatalf("failed preflight must not change cache: %d -> %d", sizeBefore, n)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("existing entry must survive failed preflight, got %+v", d)
	}
}

// 四种预检失败按 来源>凭据>方法>头 的次序只报第一个。
func TestPreflightFailureOrder(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := Request{Origin: "https://a.example", Target: "https://b.example", Method: "PUT",
		Headers: []Header{{"X-A", "1"}, {"X-B", "2"}}, Credentials: CredentialsInclude}
	resp := PreflightResponse{Headers: map[string]string{
		"Access-Control-Allow-Origin":  "https://evil.example",
		"Access-Control-Allow-Methods": "GET",
		"Access-Control-Allow-Headers": "x-c",
		"Access-Control-Max-Age":       "10",
	}}
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailOrigin)

	resp.Headers["Access-Control-Allow-Origin"] = "https://a.example"
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailCredentials)

	resp.Headers["Access-Control-Allow-Credentials"] = "true"
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailMethod)

	resp.Headers["Access-Control-Allow-Methods"] = "PUT"
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailHeader)

	resp.Headers["Access-Control-Allow-Headers"] = "x-a, x-b"
	if err := k.SubmitPreflightResponse(req, resp); err != nil {
		t.Fatalf("all fixed: %v", err)
	}
}

// 实际响应校验失败不影响缓存条目。
func TestActualResponseFailureKeepsCache(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	if err := k.SubmitPreflightResponse(req, allowPreflight("https://a.example", "PUT", "x-token", "10")); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	sizeBefore := k.CacheSize()

	bad := ActualResponse{Headers: map[string]string{
		"Access-Control-Allow-Origin": "https://evil.example",
	}}
	_, err := k.SubmitActualResponse(req, bad)
	checkErr(t, err, ErrCodeResponseValidation, FailOrigin)

	if n := k.CacheSize(); n != sizeBefore {
		t.Fatalf("actual response failure must not change cache: %d -> %d", sizeBefore, n)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("cache entry must survive actual response failure, got %+v", d)
	}

	// 凭据包含但响应未显式允许凭据 → 凭据不符。
	inc := req
	inc.Credentials = CredentialsInclude
	_, err = k.SubmitActualResponse(inc, ActualResponse{Headers: map[string]string{
		"Access-Control-Allow-Origin": "https://a.example",
	}})
	checkErr(t, err, ErrCodeResponseValidation, FailCredentials)
}

// 重定向到另一来源后发起来源变为不透明：与任何目标都不相等，
// 需预检的请求须以不透明来源为键对新目标重新预检。
func TestRedirectOpaqueOrigin(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()

	// 同源重定向：不产生不透明来源。
	same, d, err := k.SubmitRedirect(req, req.Target)
	if err != nil {
		t.Fatalf("same-target redirect: %v", err)
	}
	if same.Origin != req.Origin {
		t.Fatalf("same-target redirect must keep origin, got %q", same.Origin)
	}
	if d.Action != ActionPreflight {
		t.Fatalf("redirected request still needs preflight, got %+v", d)
	}

	r1, d1, err := k.SubmitRedirect(req, "https://c.example")
	if err != nil {
		t.Fatalf("redirect: %v", err)
	}
	if r1.Origin == req.Origin || r1.Origin == r1.Target {
		t.Fatalf("opaque origin must differ from initiator and target: %q", r1.Origin)
	}
	if d1.Action != ActionPreflight {
		t.Fatalf("preflight-required request must re-preflight new target, got %+v", d1)
	}

	r2, _, err := k.SubmitRedirect(req, "https://c.example")
	if err != nil {
		t.Fatalf("redirect 2: %v", err)
	}
	if r1.Origin == r2.Origin {
		t.Fatalf("each redirect must mint a distinct opaque origin: %q", r1.Origin)
	}

	// 旧来源的缓存对不透明来源不生效；以不透明来源为键重新预检后命中。
	if err := k.SubmitPreflightResponse(req, allowPreflight(req.Origin, "PUT", "x-token", "10")); err != nil {
		t.Fatalf("preflight original: %v", err)
	}
	if d := mustDecide(t, k, r1); d.CacheHit {
		t.Fatalf("opaque origin must not reuse old origin's cache entry, got %+v", d)
	}
	if err := k.SubmitPreflightResponse(r1, allowPreflight(r1.Origin, "PUT", "x-token", "10")); err != nil {
		t.Fatalf("preflight opaque: %v", err)
	}
	if d := mustDecide(t, k, r1); !d.CacheHit {
		t.Fatalf("opaque origin cache entry must hit, got %+v", d)
	}
}

// 容量淘汰：先清过期，再按最近命中时刻最早者淘汰，并列按创建时刻最早者。
func TestEvictionTieBreak(t *testing.T) {
	cfg := baseConfig()
	cfg.MaxEntries = 2
	mkReq := func(o, target string) Request {
		return Request{Origin: o, Target: target, Method: "PUT", Headers: []Header{{"X-A", "1"}}}
	}
	allow := func(o string) PreflightResponse { return allowPreflight(o, "PUT", "x-a", "10") }

	// 并列（都未命中过）：淘汰创建时刻最早者 A。
	k := newKernel(t, cfg)
	reqA, reqB, reqC := mkReq("https://o1.example", "https://t1.example"),
		mkReq("https://o2.example", "https://t2.example"),
		mkReq("https://o3.example", "https://t3.example")
	for _, r := range []Request{reqA, reqB} {
		if err := k.SubmitPreflightResponse(r, allow(r.Origin)); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	}
	if err := k.SubmitPreflightResponse(reqC, allow(reqC.Origin)); err != nil {
		t.Fatalf("preflight C: %v", err)
	}
	if d := mustDecide(t, k, reqA); d.CacheHit {
		t.Fatalf("A (earliest created) must be evicted on tie, got %+v", d)
	}
	if d := mustDecide(t, k, reqB); !d.CacheHit {
		t.Fatalf("B must survive, got %+v", d)
	}
	if d := mustDecide(t, k, reqC); !d.CacheHit {
		t.Fatalf("C must be cached, got %+v", d)
	}

	// 非并列：淘汰最近命中时刻最早者 B（A 刚命中过）。
	k2 := newKernel(t, cfg)
	for _, r := range []Request{reqA, reqB} {
		if err := k2.SubmitPreflightResponse(r, allow(r.Origin)); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	}
	if err := k2.Advance(1); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k2, reqA); !d.CacheHit {
		t.Fatalf("A must hit, got %+v", d)
	}
	if err := k2.SubmitPreflightResponse(reqC, allow(reqC.Origin)); err != nil {
		t.Fatalf("preflight C: %v", err)
	}
	if d := mustDecide(t, k2, reqB); d.CacheHit {
		t.Fatalf("B (oldest last-hit) must be evicted, got %+v", d)
	}
	if d := mustDecide(t, k2, reqA); !d.CacheHit {
		t.Fatalf("A (recently hit) must survive, got %+v", d)
	}

	// 先清过期条目：C 过期后插入 D 不淘汰存活条目。
	if err := k2.Advance(10); err != nil { // 全部过期
		t.Fatal(err)
	}
	reqD := mkReq("https://o4.example", "https://t4.example")
	if err := k2.SubmitPreflightResponse(reqD, allow(reqD.Origin)); err != nil {
		t.Fatalf("preflight D: %v", err)
	}
	if n := k2.CacheSize(); n != 1 {
		t.Fatalf("expired entries must be purged before eviction, size=%d", n)
	}
}

// 按发起来源或目标来源清除。
func TestPurgeByOriginAndTarget(t *testing.T) {
	k := newKernel(t, baseConfig())
	mk := func(o, target string) Request {
		return Request{Origin: o, Target: target, Method: "PUT", Headers: []Header{{"X-A", "1"}}}
	}
	reqs := []Request{
		mk("https://o1.example", "https://t1.example"),
		mk("https://o1.example", "https://t2.example"),
		mk("https://o2.example", "https://t1.example"),
	}
	for _, r := range reqs {
		if err := k.SubmitPreflightResponse(r, allowPreflight(r.Origin, "PUT", "x-a", "10")); err != nil {
			t.Fatalf("preflight: %v", err)
		}
	}
	if n := k.PurgeByOrigin("https://o1.example"); n != 2 {
		t.Fatalf("purge by origin: want 2, got %d", n)
	}
	if n := k.CacheSize(); n != 1 {
		t.Fatalf("after purge by origin: want 1, got %d", n)
	}
	if n := k.PurgeByTarget("https://t1.example"); n != 1 {
		t.Fatalf("purge by target: want 1, got %d", n)
	}
	if n := k.CacheSize(); n != 0 {
		t.Fatalf("after purge by target: want 0, got %d", n)
	}
}

// 参数非法：空来源、空方法、头名字含非法字符、上限非正；且优先于重定向不允许。
func TestInvalidArguments(t *testing.T) {
	k := newKernel(t, baseConfig())
	good := preflightReq()

	bad := good
	bad.Origin = ""
	checkErr(t, mustDecideErr(k, bad), ErrCodeInvalidArgument, FailNone)

	bad = good
	bad.Method = ""
	checkErr(t, mustDecideErr(k, bad), ErrCodeInvalidArgument, FailNone)

	bad = good
	bad.Target = ""
	checkErr(t, mustDecideErr(k, bad), ErrCodeInvalidArgument, FailNone)

	bad = good
	bad.Headers = []Header{{"Bad Name", "v"}}
	checkErr(t, mustDecideErr(k, bad), ErrCodeInvalidArgument, FailNone)

	bad = good
	bad.Headers = []Header{{"", "v"}}
	checkErr(t, mustDecideErr(k, bad), ErrCodeInvalidArgument, FailNone)

	// 上限非正。
	for _, mutate := range []func(*Config){
		func(c *Config) { c.MaxEntries = 0 },
		func(c *Config) { c.MaxAgeMax = 0 },
		func(c *Config) { c.MaxAgeMax = -1 },
		func(c *Config) { c.MaxAgeDefault = -1 },
		func(c *Config) { c.SafeHeaders["Accept"] = HeaderRule{MaxLen: 0} },
	} {
		cfg := baseConfig()
		mutate(&cfg)
		if _, err := NewKernel(cfg); err == nil {
			t.Fatalf("want invalid-argument for non-positive limit")
		} else {
			checkErr(t, err, ErrCodeInvalidArgument, FailNone)
		}
	}

	// 参数非法优先于重定向不允许。
	bad = good
	bad.Method = ""
	checkErr(t, k.SubmitPreflightResponse(bad, PreflightResponse{Redirect: true}),
		ErrCodeInvalidArgument, FailNone)
}

func mustDecideErr(k *Kernel, req Request) error {
	_, err := k.Decide(req)
	if err == nil {
		panic("want error")
	}
	return err
}

// 时钟回退：拒绝且时钟不变。
func TestClockRollback(t *testing.T) {
	k := newKernel(t, baseConfig())
	if err := k.Advance(5); err != nil {
		t.Fatal(err)
	}
	checkErr(t, k.Advance(-2), ErrCodeClockRollback, FailNone)
	if now := k.Now(); now != 5 {
		t.Fatalf("clock must be unchanged after rollback rejection, got %d", now)
	}
}

// 预检遇到重定向一律失败，且不改动缓存。
func TestPreflightRedirectFails(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	checkErr(t, k.SubmitPreflightResponse(req, PreflightResponse{Redirect: true}),
		ErrCodeRedirectNotAllowed, FailNone)
	if n := k.CacheSize(); n != 0 {
		t.Fatalf("redirected preflight must not touch cache, size=%d", n)
	}
}

// 被拒绝的操作不得改变缓存与时钟。
func TestRejectionDoesNotMutate(t *testing.T) {
	k := newKernel(t, baseConfig())
	req := preflightReq()
	if err := k.SubmitPreflightResponse(req, allowPreflight("https://a.example", "PUT", "x-token", "10")); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := k.Advance(3); err != nil {
		t.Fatal(err)
	}
	now0, size0 := k.Now(), k.CacheSize()

	bad := req
	bad.Method = ""
	_, _ = k.Decide(bad)                                                                               // 参数非法
	_ = k.Advance(-1)                                                                                  // 时钟回退
	_ = k.SubmitPreflightResponse(req, PreflightResponse{Redirect: true})                              // 重定向不允许
	_ = k.SubmitPreflightResponse(req, allowPreflight("https://evil.example", "PUT", "x-token", "10")) // 预检失败
	_, _ = k.SubmitActualResponse(req, ActualResponse{Headers: map[string]string{
		"Access-Control-Allow-Origin": "https://evil.example"}}) // 响应校验失败

	if now := k.Now(); now != now0 {
		t.Fatalf("clock changed by rejected ops: %d -> %d", now0, now)
	}
	if size := k.CacheSize(); size != size0 {
		t.Fatalf("cache changed by rejected ops: %d -> %d", size0, size)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("entry must be intact after rejected ops, got %+v", d)
	}
}
