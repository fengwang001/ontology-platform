package cors

import (
	"errors"
	"testing"
)

func baseConfig() Config {
	return Config{
		SafeMethods: []string{"GET", "HEAD", "POST"},
		SafeHeaders: map[string]HeaderRule{
			"Accept": {MaxLen: 32},
			"Content-Type": {MaxLen: 16, Allowed: func(b byte) bool {
				return b == '/' || b == '-' || ('a' <= b && b <= 'z') || ('0' <= b && b <= '9')
			}},
		},
		SafeResponseHeaders: []string{"Cache-Control", "Content-Type"},
		MaxEntries:          4,
		MaxAgeDefault:       5,
		MaxAgeMax:           10,
	}
}

func newKernel(t *testing.T, cfg Config) *Kernel {
	t.Helper()
	cfg.Logf = func(format string, args ...any) { t.Logf(format, args...) }
	k, err := NewKernel(cfg)
	if err != nil {
		t.Fatalf("NewKernel: %v", err)
	}
	return k
}

func preflightReq() Request {
	return Request{
		Origin:      "https://a.example",
		Target:      "https://b.example",
		Method:      "PUT",
		Headers:     []Header{{Name: "X-Token", Value: "abc"}},
		Credentials: CredentialsOmit,
	}
}

// allowPreflight 构造一个允许给定方法与头、存活时长为 maxAge 的预检响应。
func allowPreflight(origin, methods, headers, maxAge string) PreflightResponse {
	h := map[string]string{
		"Access-Control-Allow-Origin":  origin,
		"Access-Control-Allow-Methods": methods,
		"Access-Control-Allow-Headers": headers,
	}
	if maxAge != "" {
		h["Access-Control-Max-Age"] = maxAge
	}
	return PreflightResponse{Headers: h}
}

func checkErr(t *testing.T, err error, code ErrorCode, reason FailReason) {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *Error, got %T: %v", err, err)
	}
	if ce.Code != code || ce.Reason != reason {
		t.Fatalf("want (%v,%v), got (%v,%v): %v", code, reason, ce.Code, ce.Reason, err)
	}
}

func mustDecide(t *testing.T, k *Kernel, req Request) Decision {
	t.Helper()
	d, err := k.Decide(req)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	return d
}

// 三条简单条件逐一不满足：方法不安全 / 头不安全 / 头值违反约束。
func TestClassifySimpleConditions(t *testing.T) {
	k := newKernel(t, baseConfig())
	origin, target := "https://a.example", "https://b.example"

	simple := Request{Origin: origin, Target: target, Method: "GET",
		Headers: []Header{{"Accept", "x"}, {"Content-Type", "text/plain"}}}
	if d := mustDecide(t, k, simple); d.Action != ActionSend || !d.Simple || d.CacheHit {
		t.Fatalf("simple request: got %+v", d)
	}

	badMethod := simple
	badMethod.Method = "PUT"
	if d := mustDecide(t, k, badMethod); d.Action != ActionPreflight {
		t.Fatalf("unsafe method: got %+v", d)
	}

	badHeader := simple
	badHeader.Headers = []Header{{"Accept", "x"}, {"X-Token", "v"}}
	if d := mustDecide(t, k, badHeader); d.Action != ActionPreflight {
		t.Fatalf("unsafe header: got %+v", d)
	}

	tooLong := simple
	tooLong.Headers = []Header{{"Content-Type", "application/json-v2"}} // 19 > 16
	if d := mustDecide(t, k, tooLong); d.Action != ActionPreflight {
		t.Fatalf("value too long: got %+v", d)
	}

	badCharset := simple
	badCharset.Headers = []Header{{"Content-Type", "text/plain!"}} // '!' 不在允许字符集
	if d := mustDecide(t, k, badCharset); d.Action != ActionPreflight {
		t.Fatalf("bad charset: got %+v", d)
	}
}

// 头名字大小写差异：请求头、配置安全头、响应头均不区分大小写。
func TestHeaderNameCaseInsensitive(t *testing.T) {
	k := newKernel(t, baseConfig())

	simple := Request{Origin: "https://a.example", Target: "https://b.example", Method: "GET",
		Headers: []Header{{"ACCEPT", "x"}, {"content-TYPE", "text/plain"}}}
	if d := mustDecide(t, k, simple); d.Action != ActionSend || !d.Simple {
		t.Fatalf("case-insensitive safe headers: got %+v", d)
	}

	req := preflightReq()
	req.Headers = []Header{{"X-TOKEN", "abc"}}
	resp := PreflightResponse{Headers: map[string]string{
		"ACCESS-CONTROL-ALLOW-ORIGIN":  "https://a.example",
		"Access-Control-ALLOW-Methods": "put",
		"access-control-allow-headers": "X-Token",
		"ACCESS-CONTROL-MAX-AGE":       "10",
	}}
	// 方法区分大小写：响应只允许 "put" 而请求是 "PUT"，应报方法不符。
	checkErr(t, k.SubmitPreflightResponse(req, resp), ErrCodePreflightFailed, FailMethod)

	resp.Headers["Access-Control-ALLOW-Methods"] = "PUT"
	if err := k.SubmitPreflightResponse(req, resp); err != nil {
		t.Fatalf("preflight with mixed-case names: %v", err)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("want cache hit, got %+v", d)
	}
}

// 存活时长为零与负：不缓存，结果只用于当前请求。
func TestMaxAgeZeroAndNegative(t *testing.T) {
	for _, maxAge := range []string{"0", "-3"} {
		t.Run("maxAge="+maxAge, func(t *testing.T) {
			k := newKernel(t, baseConfig())
			req := preflightReq()
			resp := allowPreflight("https://a.example", "PUT", "x-token", maxAge)
			if err := k.SubmitPreflightResponse(req, resp); err != nil {
				t.Fatalf("preflight should succeed for this request: %v", err)
			}
			if n := k.CacheSize(); n != 0 {
				t.Fatalf("max-age %s must not be cached, cache size=%d", maxAge, n)
			}
			if d := mustDecide(t, k, req); d.Action != ActionPreflight || d.CacheHit {
				t.Fatalf("max-age %s: identical request must preflight again, got %+v", maxAge, d)
			}
		})
	}
}

// 存活时长受上限夹住；过期时刻恰等于当前时刻视为已过期。
func TestMaxAgeClampedAndExpiryEqualsNow(t *testing.T) {
	k := newKernel(t, baseConfig()) // MaxAgeMax=10
	req := preflightReq()
	resp := allowPreflight("https://a.example", "PUT", "x-token", "1000")
	if err := k.SubmitPreflightResponse(req, resp); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := k.Advance(9); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("t=9 < expiry=10: want cache hit, got %+v", d)
	}
	if err := k.Advance(1); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k, req); d.CacheHit || d.Action != ActionPreflight {
		t.Fatalf("t=10 == expiry: entry must be expired, got %+v", d)
	}
}

// 未声明存活时长时取默认值。
func TestMaxAgeDefault(t *testing.T) {
	k := newKernel(t, baseConfig()) // default=5, max=10
	req := preflightReq()
	resp := allowPreflight("https://a.example", "PUT", "x-token", "")
	if err := k.SubmitPreflightResponse(req, resp); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := k.Advance(4); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k, req); !d.CacheHit {
		t.Fatalf("t=4 < expiry=5: want cache hit, got %+v", d)
	}
	if err := k.Advance(1); err != nil {
		t.Fatal(err)
	}
	if d := mustDecide(t, k, req); d.CacheHit {
		t.Fatalf("t=5 == expiry: want miss, got %+v", d)
	}
}
