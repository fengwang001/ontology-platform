package verify

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"ontology/canon"
	"ontology/cred"
)

func baseHeaders(ts int64) []canon.Header {
	return []canon.Header{
		{Name: "Host", Value: "h.example"},
		{Name: "X-Date", Value: strconv.FormatInt(ts, 10)},
	}
}

func setupAndSignHeader(t *testing.T, s *cred.Store, req *Request, tenant, keyID, secret string, rotateNow int64) {
	t.Helper()
	if err := s.Rotate(tenant, keyID, secret, req.Region, req.Service, 100000, rotateNow); err != nil {
		t.Fatal(err)
	}
	req.KeyID = keyID
	c, err := canon.Build(req.Method, req.Path, req.Query, req.Headers, req.SignedNames, req.PayloadHash)
	if err != nil {
		t.Fatal(err)
	}
	req.Signature = sign(secret, req.Region, req.Service, req.TS, c)
}

func TestHeaderBoundary900(t *testing.T) {
	cases := []struct {
		name    string
		ts, now int64
		wantErr error
	}{
		{"偏差恰等900未来", 1000, 1900, nil},
		{"偏差恰等900过去", 1900, 1000, nil},
		{"偏差901未来", 1000, 1901, ErrSkew},
		{"偏差901过去", 1901, 1000, ErrSkew},
		{"同时刻", 1000, 1000, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cred.NewStore()
			v := New(s)
			req := Request{
				Method: "GET", Path: "/", Headers: baseHeaders(tc.ts),
				SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
				TS: tc.ts, Region: "r", Service: "svc", Mode: Header,
			}
			setupAndSignHeader(t, s, &req, "T", "k1", "sec", tc.ts-10)
			_, err := v.Verify(req, tc.now)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			t.Logf("ts=%d now=%d => %v（依据：绝对差>900 报偏差，恰等 900 通过）", tc.ts, tc.now, err)
		})
	}
}

func TestGraceAndRevokeSpec(t *testing.T) {
	s := cred.NewStore()
	v := New(s)
	mk := func(ts int64, keyID, secret string) Request {
		req := Request{
			Method: "GET", Path: "/", Headers: baseHeaders(ts),
			SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
			TS: ts, Region: "r", Service: "svc", Mode: Header, KeyID: keyID,
		}
		c, _ := canon.Build(req.Method, req.Path, req.Query, req.Headers, req.SignedNames, req.PayloadHash)
		req.Signature = sign(secret, req.Region, req.Service, ts, c)
		return req
	}
	if err := s.Rotate("T", "k1", "sec", "r", "svc", 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Rotate("T", "k2", "sec", "r", "svc", 100, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(mk(149, "k1", "sec"), 149); err != nil {
		t.Fatalf("k1 now=149 应通过: %v", err)
	}
	if _, err := v.Verify(mk(150, "k1", "sec"), 150); !errors.Is(err, ErrCredentialUnusable) {
		t.Fatalf("k1 now=150: %v", err)
	}
	pre := func(ts, now, expires int64) (Result, error) {
		kept := [][2]string{{"X-Expires", strconv.FormatInt(expires, 10)}}
		req := Request{
			Method: "GET", Path: "/", Headers: baseHeaders(ts),
			SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
			TS: ts, Region: "r", Service: "svc", Mode: Presigned, Expires: expires, KeyID: "k1",
			Query: kept,
		}
		c, _ := canon.Build(req.Method, req.Path, kept, req.Headers, req.SignedNames, req.PayloadHash)
		sig := sign("sec", "r", "svc", ts, c)
		req.Query = append(req.Query, [2]string{"X-Sig", sig})
		return v.Verify(req, now)
	}
	if _, err := pre(100, 500, 1000); err != nil {
		t.Fatalf("预签名 ts=100 now=500 应通过: %v", err)
	}
	if _, err := pre(150, 500, 1000); !errors.Is(err, ErrCredentialUnusable) {
		t.Fatalf("预签名 ts=150 应报凭证不可用: %v", err)
	}
	if err := s.RevokeBefore("T", 120, 160); err != nil {
		t.Fatal(err)
	}
	if _, err := pre(100, 200, 1000); !errors.Is(err, ErrRevoked) {
		t.Fatalf("ts=100 撤销后应报已撤销: %v", err)
	}
	if _, err := pre(120, 200, 1000); err != nil {
		t.Fatalf("ts=120 恰等撤销线应通过: %v", err)
	}
	t.Logf("宽限期：Header 按 now（149 通过/150 不可用），Presigned 按 ts；撤销 ts<120 无效 ts==120 有效")
}

func TestPresignedExpiryAndSigRemoval(t *testing.T) {
	s := cred.NewStore()
	v := New(s)
	if err := s.Rotate("T", "k1", "sec", "r", "svc", 100000, 0); err != nil {
		t.Fatal(err)
	}
	build := func(ts, now, expires int64, removeSigFromCanon bool) Request {
		req := Request{
			Method: "GET", Path: "/", Headers: baseHeaders(ts),
			SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
			TS: ts, Region: "r", Service: "svc", Mode: Presigned, Expires: expires, KeyID: "k1",
		}
		canonQuery := [][2]string{{"X-Expires", strconv.FormatInt(expires, 10)}}
		if !removeSigFromCanon {
			canonQuery = [][2]string{{"X-Sig", "sig-placeholder"}, {"X-Expires", strconv.FormatInt(expires, 10)}}
		}
		c, _ := canon.Build(req.Method, req.Path, canonQuery, req.Headers, req.SignedNames, req.PayloadHash)
		sig := sign("sec", "r", "svc", ts, c)
		req.Query = [][2]string{{"X-Expires", strconv.FormatInt(expires, 10)}, {"X-Sig", sig}}
		return req
	}
	if _, err := v.Verify(build(100, 1099, 1000, true), 1099); err != nil {
		t.Fatalf("恰到期前一刻应通过: %v", err)
	}
	if _, err := v.Verify(build(100, 1100, 1000, true), 1100); !errors.Is(err, ErrExpired) {
		t.Fatalf("now==ts+expires 应已过期: %v", err)
	}
	if _, err := v.Verify(build(5000, 2000, 604800, true), 2000); !errors.Is(err, ErrSkew) {
		t.Fatalf("ts>now+900 应报偏差: %v", err)
	}
	if _, err := v.Verify(build(100, 200, 1000, false), 200); !errors.Is(err, ErrSignature) {
		t.Fatalf("未删除 X-Sig 规范化应报签名不符: %v", err)
	}
	req := build(100, 200, 1000, true)
	req.PayloadHash = "hash"
	if _, err := v.Verify(req, 200); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Presigned 非 UNSIGNED: %v", err)
	}
	// X-Sig 必须恰有一个。
	req = build(100, 200, 1000, true)
	req.Query = append(req.Query, [2]string{"X-Sig", strings.Repeat("a", 64)})
	if _, err := v.Verify(req, 200); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("两个 X-Sig 应参数非法: %v", err)
	}
	t.Logf("Presigned：now==ts+expires 恰到期报已过期；规范串必须删除 X-Sig；payload 须 UNSIGNED；X-Sig 须恰一个")
}

func TestRejectOrder(t *testing.T) {
	s := cred.NewStore()
	v := New(s)
	if err := s.Rotate("T", "k1", "sec", "r", "svc", 100000, 0); err != nil {
		t.Fatal(err)
	}
	good := func() Request {
		req := Request{
			Method: "GET", Path: "/", Headers: baseHeaders(100),
			SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
			TS: 100, Region: "r", Service: "svc", Mode: Header, KeyID: "k1",
		}
		c, _ := canon.Build(req.Method, req.Path, nil, req.Headers, req.SignedNames, req.PayloadHash)
		req.Signature = sign("sec", "r", "svc", 100, c)
		return req
	}
	req := good()
	req.Method = "get"
	req.KeyID = "missing"
	req.Signature = strings.Repeat("0", 64)
	if _, err := v.Verify(req, 100); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("规范化错误应优先: %v", err)
	}
	req = good()
	req.KeyID = "missing"
	if _, err := v.Verify(req, 100); !errors.Is(err, ErrCredentialMissing) {
		t.Fatalf("凭证不存在优先: %v", err)
	}
	if err := s.Disable("k1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeBefore("T", 1000, 2); err != nil {
		t.Fatal(err)
	}
	req = good()
	req.Region = "other"
	req.Signature = strings.Repeat("f", 64)
	if _, err := v.Verify(req, 5000); !errors.Is(err, ErrCredentialUnusable) {
		t.Fatalf("已停用优先于撤销/时间/作用域/签名: %v", err)
	}
	t.Logf("拒绝次序：参数非法 > 凭证不存在 > 凭证不可用（同时叠加已撤销+偏差+作用域不符+错签名）")
}

func TestXDateMustMatchTS(t *testing.T) {
	s := cred.NewStore()
	v := New(s)
	req := Request{
		Method: "GET", Path: "/",
		Headers: []canon.Header{
			{Name: "Host", Value: "h"},
			{Name: "X-Date", Value: "101"},
		},
		SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
		TS: 100, Region: "r", Service: "svc", Mode: Header, KeyID: "k1",
		Signature: strings.Repeat("0", 64),
	}
	if _, err := v.Verify(req, 100); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("x-date 与 ts 不一致: %v", err)
	}
}

func TestProbeCount(t *testing.T) {
	s := cred.NewStore()
	v := New(s)
	if err := s.Rotate("T", "k1", "sec", "r", "svc", 100000, 0); err != nil {
		t.Fatal(err)
	}
	req := Request{
		Method: "GET", Path: "/", Headers: baseHeaders(100),
		SignedNames: []string{"host", "x-date"}, PayloadHash: "UNSIGNED",
		TS: 100, Region: "r", Service: "svc", Mode: Header, KeyID: "k1",
	}
	c, _ := canon.Build(req.Method, req.Path, nil, req.Headers, req.SignedNames, req.PayloadHash)
	req.Signature = sign("sec", "r", "svc", 100, c)
	cred.ResetProbesForTest()
	if _, err := v.Verify(req, 100); err != nil {
		t.Fatal(err)
	}
	if probes := cred.ProbesForTest(); probes != 1 {
		t.Fatalf("credProbes=%d want 1", probes)
	}
	t.Logf("一次 Verify 的 credProbes=1")
}
