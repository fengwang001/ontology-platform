package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/canon"
	"ontology/cred"
)

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func mustRotate(t *testing.T, s *cred.Store, tenant, keyID, secret, region, service string, g, now int64) {
	t.Helper()
	if err := s.Rotate(tenant, keyID, secret, region, service, g, now); err != nil {
		t.Fatalf("rotate: %v", err)
	}
}

func signReq(t *testing.T, c cred.Credential, req Request) Request {
	t.Helper()
	return signWithSecret(c.Secret, req)
}

// signWithSecret 按四层 HMAC 组装并写入签名（Header 写 Signature；Presigned 写 X-Sig 查询参数）。
func signWithSecret(secret string, req Request) Request {
	query := req.Query
	if req.Mode == Presigned {
		kept := make([]canon.Pair, 0, len(query)+1)
		kept = append(kept, canon.Pair{Name: "X-Expires", Value: strconv.FormatInt(req.Expires, 10)})
		for _, q := range query {
			if q.Name != "X-Sig" && q.Name != "X-Expires" {
				kept = append(kept, q)
			}
		}
		query = kept
	}
	canonical, err := canon.Build(req.Method, req.Path, query, req.Headers, req.SignedNames, req.PayloadHash)
	if err != nil {
		panic(err)
	}
	day := strconv.FormatInt(req.TS/86400, 10)
	scope := day + "/" + req.Region + "/" + req.Service
	sts := "KS4-HMAC-SHA256\n" + strconv.FormatInt(req.TS, 10) + "\n" + scope + "\n" + sha256Hex(canonical)
	sig := ComputeSignature(secret, req.TS, req.Region, req.Service, sts)
	if req.Mode == Presigned {
		query = append(query, canon.Pair{Name: "X-Sig", Value: sig})
		req.Signature = ""
	} else {
		req.Signature = sig
	}
	req.Query = query
	return req
}

func baseReq(mode Mode) Request {
	return Request{
		KeyID:       "k1",
		Method:      "GET",
		Path:        "/obj",
		Headers:     []canon.Pair{{Name: "Host", Value: "h.example"}, {Name: "X-Date", Value: "1000"}},
		SignedNames: []string{"host", "x-date"},
		PayloadHash: "UNSIGNED",
		TS:          1000,
		Region:      "r",
		Service:     "svc",
		Mode:        mode,
		Expires:     600,
	}
}

func TestVerifyTable(t *testing.T) {
	type want struct {
		tenant, keyID string
		err           error
	}
	cases := []struct {
		name  string
		setup func(t *testing.T, s *cred.Store) cred.Credential
		req   func(t *testing.T, c cred.Credential) Request
		now   int64
		want  want
	}{
		{
			name:  "header happy",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:   1000,
			want:  want{"T", "k1", nil},
		},
		{
			name:  "skew equal 900 passes",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:   1900,
			want:  want{"T", "k1", nil},
		},
		{
			name:  "skew 901 fails",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:   1901,
			want:  want{err: ErrSkew},
		},
		{
			name:  "presigned happy",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Presigned)) },
			now:   1100,
			want:  want{"T", "k1", nil},
		},
		{
			name:  "presigned one second before expiry passes",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				return signReq(t, c, baseReq(Presigned))
			},
			now:  1599,
			want: want{"T", "k1", nil},
		},
		{
			name:  "presigned exactly at expiry fails",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Presigned)) },
			now:   1600,
			want:  want{err: ErrExpired},
		},
		{
			name:  "presigned one past expiry fails",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Presigned)) },
			now:   1601,
			want:  want{err: ErrExpired},
		},
		{
			name:  "presigned future ts beyond skew",
			setup: activeK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Presigned)) },
			now:   99,
			want:  want{err: ErrSkew},
		},
		{
			name:  "retiring header before grace passes",
			setup: retiredK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:   149,
			want:  want{"T", "k1", nil},
		},
		{
			name:  "retiring header at grace fails",
			setup: retiredK1,
			req:   func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:   150,
			want:  want{err: ErrCredentialStale},
		},
		{
			name:  "retiring presigned signed before grace survives",
			setup: retiredK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.TS = 100
				r.Headers = []canon.Pair{{Name: "Host", Value: "h.example"}, {Name: "X-Date", Value: "100"}}
				r.Expires = 1000
				return signReq(t, c, r)
			},
			now:  500,
			want: want{"T", "k1", nil},
		},
		{
			name:  "retiring presigned signed at grace fails",
			setup: retiredK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.TS = 150
				r.Headers = []canon.Pair{{Name: "Host", Value: "h.example"}, {Name: "X-Date", Value: "150"}}
				r.Expires = 1000
				return signReq(t, c, r)
			},
			now:  200,
			want: want{err: ErrCredentialStale},
		},
		{
			name:  "revoke ts below boundary rejected",
			setup: revokedK1(120),
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.TS = 100
				r.Headers = []canon.Pair{{Name: "Host", Value: "h.example"}, {Name: "X-Date", Value: "100"}}
				r.Expires = 1000
				return signReq(t, c, r)
			},
			now:  200,
			want: want{err: ErrRevoked},
		},
		{
			name:  "revoke ts equal boundary passes",
			setup: revokedK1(120),
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.TS = 120
				r.Headers = []canon.Pair{{Name: "Host", Value: "h.example"}, {Name: "X-Date", Value: "120"}}
				r.Expires = 1000
				return signReq(t, c, r)
			},
			now:  200,
			want: want{"T", "k1", nil},
		},
		{
			name:  "unknown key",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Header))
				r.KeyID = "ghost"
				return r
			},
			now:  1000,
			want: want{err: ErrCredentialGone},
		},
		{
			name: "disabled credential",
			setup: func(t *testing.T, s *cred.Store) cred.Credential {
				c := activeK1(t, s)
				if err := s.Disable("k1", 1); err != nil {
					t.Fatal(err)
				}
				c, _ = s.Snapshot("k1")
				return c
			},
			req:  func(t *testing.T, c cred.Credential) Request { return signReq(t, c, baseReq(Header)) },
			now:  1000,
			want: want{err: ErrCredentialStale},
		},
		{
			name:  "scope mismatch region",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Header))
				r.Region = "other"
				return r
			},
			now:  1000,
			want: want{err: ErrScope},
		},
		{
			name:  "bad signature",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Header))
				r.Signature = "0000000000000000000000000000000000000000000000000000000000000000"
				return r
			},
			now:  1000,
			want: want{err: ErrSignature},
		},
		{
			name:  "x-date mismatch is invalid param",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Header))
				r.Headers[1].Value = "1001"
				return r
			},
			now:  1000,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "presigned two xsigs invalid",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Presigned))
				r.Query = append(r.Query, canon.Pair{Name: "X-Sig", Value: "ab"})
				return r
			},
			now:  1100,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "presigned payload not unsigned invalid",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.PayloadHash = "deadbeef"
				return signReq(t, c, r)
			},
			now:  1100,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "presigned expires zero invalid",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.Expires = 0
				return signReq(t, c, r)
			},
			now:  1100,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "presigned expires over week invalid",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Presigned)
				r.Expires = 604801
				return signReq(t, c, r)
			},
			now:  1100,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "x-sig must be removed before canonicalization",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				// Header 模式下 X-Sig 是普通参数：若按 Presigned 规则签则必失败。
				r := baseReq(Presigned)
				signed := signReq(t, c, r)
				r2 := baseReq(Header)
				r2.Query = signed.Query
				r2.Signature = ""
				return r2
			},
			now:  1100,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "header mode keeps xsig as ordinary query and passes",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := baseReq(Header)
				r.Query = []canon.Pair{{Name: "X-Sig", Value: "anything"}}
				return signReq(t, c, r)
			},
			now:  1000,
			want: want{"T", "k1", nil},
		},
		{
			name:  "invalid param beats credential gone",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				r := signReq(t, c, baseReq(Header))
				r.KeyID = "ghost"
				r.Method = "get"
				return r
			},
			now:  1000,
			want: want{err: ErrInvalidParam},
		},
		{
			name:  "credential gone beats stale time",
			setup: activeK1,
			req: func(t *testing.T, c cred.Credential) Request {
				return signReq(t, c, baseReq(Header))
			},
			now:  100000,
			want: want{err: ErrSkew}, // 存在的凭证先报偏差，验证时间类位置
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := cred.NewStore()
			c := tc.setup(t, s)
			req := tc.req(t, c)
			tenant, keyID, err := New(s).Verify(req, tc.now)
			if !errors.Is(err, tc.want.err) {
				t.Fatalf("err=%v want %v", err, tc.want.err)
			}
			if err == nil && (tenant != tc.want.tenant || keyID != tc.want.keyID) {
				t.Fatalf("tenant=%q keyID=%q want %q,%q", tenant, keyID, tc.want.tenant, tc.want.keyID)
			}
		})
	}
}

func activeK1(t *testing.T, s *cred.Store) cred.Credential {
	mustRotate(t, s, "T", "k1", "sec", "r", "svc", 100, 0)
	c, _ := s.Snapshot("k1")
	return c
}

func retiredK1(t *testing.T, s *cred.Store) cred.Credential {
	mustRotate(t, s, "T", "k1", "sec", "r", "svc", 100, 0)
	mustRotate(t, s, "T", "k2", "sec", "r", "svc", 100, 50)
	c, _ := s.Snapshot("k1")
	return c
}

func revokedK1(boundary int64) func(t *testing.T, s *cred.Store) cred.Credential {
	return func(t *testing.T, s *cred.Store) cred.Credential {
		mustRotate(t, s, "T", "k1", "sec", "r", "svc", 1000, 0)
		if err := s.RevokeBefore("T", boundary, 50); err != nil {
			t.Fatal(err)
		}
		c, _ := s.Snapshot("k1")
		return c
	}
}

type naiveCred struct {
	tenant, secret, region, service string
	status                          string // active/retiring/disabled
	validUntil                      int64
	revokeBefore                    int64
}

func naiveEncode(s string, keepSlash bool) string {
	const hexU = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		alnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		switch {
		case alnum || c == '-' || c == '.' || c == '_' || c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hexU[c>>4])
			b.WriteByte(hexU[c&15])
		}
	}
	return b.String()
}

func naiveFold(v string) string {
	v = strings.TrimSpace(v)
	f := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' })
	return strings.Join(f, " ")
}

func naiveCanonical(method, path string, query []naivePair, headers []naivePair, signed []string, payload string) (string, bool) {
	if method == "" {
		return "", false
	}
	for i := 0; i < len(method); i++ {
		c := method[i]
		if c < 'A' || c > 'Z' {
			return "", false
		}
	}
	if path == "" || path[0] != '/' {
		return "", false
	}
	groups := map[string][]string{}
	firstOrder := map[string]int{}
	idx := 0
	for _, h := range headers {
		ln := strings.ToLower(h.name)
		if _, ok := groups[ln]; !ok {
			firstOrder[ln] = idx
			idx++
		}
		groups[ln] = append(groups[ln], naiveFold(h.value))
	}
	uniq := map[string]bool{}
	var names []string
	for _, n := range signed {
		ln := strings.ToLower(n)
		if !uniq[ln] {
			uniq[ln] = true
			names = append(names, ln)
		}
	}
	sort.Strings(names)
	if !uniq["host"] || !uniq["x-date"] {
		return "", false
	}
	for _, n := range names {
		if len(groups[n]) == 0 {
			return "", false
		}
	}
	type ep struct{ n, v string }
	var eqs []ep
	for _, q := range query {
		eqs = append(eqs, ep{naiveEncode(q.name, false), naiveEncode(q.value, false)})
	}
	sort.SliceStable(eqs, func(i, j int) bool {
		if eqs[i].n != eqs[j].n {
			return eqs[i].n < eqs[j].n
		}
		return eqs[i].v < eqs[j].v
	})
	var b strings.Builder
	b.WriteString(method)
	b.WriteByte('\n')
	b.WriteString(naiveEncode(path, true))
	b.WriteByte('\n')
	for i, p := range eqs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(p.n)
		b.WriteByte('=')
		b.WriteString(p.v)
	}
	b.WriteByte('\n')
	for _, n := range names {
		b.WriteString(n)
		b.WriteByte(':')
		b.WriteString(strings.Join(groups[n], ","))
		b.WriteByte('\n')
	}
	b.WriteString(strings.Join(names, ";"))
	b.WriteByte('\n')
	b.WriteString(payload)
	return b.String(), true
}

type naivePair struct{ name, value string }

func naiveHMAC(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// naiveVerify 是与 verify.Verify 语义逐字对齐、但独立书写的朴素判定。
// 返回 (ok, reason)。
func naiveVerify(db map[string]naiveCred, req Request, now int64) (bool, string) {
	if req.KeyID == "" || req.Region == "" || req.Service == "" ||
		req.TS < 0 || req.TS > maxTS || now < 0 || now > maxTS ||
		(req.Mode != Header && req.Mode != Presigned) {
		return false, "invalid-param"
	}
	var nq []naivePair
	sig := req.Signature
	if req.Mode == Presigned {
		var xsigs []string
		for _, q := range req.Query {
			if q.Name == "X-Sig" {
				xsigs = append(xsigs, q.Value)
				continue
			}
			nq = append(nq, naivePair{q.Name, q.Value})
		}
		if len(xsigs) != 1 {
			return false, "invalid-param"
		}
		sig = xsigs[0]
		expStr := strconv.FormatInt(req.Expires, 10)
		sawExp := false
		for _, q := range nq {
			if q.name == "X-Expires" {
				sawExp = true
				if q.value != expStr {
					return false, "invalid-param"
				}
			}
		}
		if !sawExp || req.Expires < 1 || req.Expires > 604800 || req.PayloadHash != "UNSIGNED" {
			return false, "invalid-param"
		}
	} else {
		for _, q := range req.Query {
			nq = append(nq, naivePair{q.Name, q.Value})
		}
	}
	if len(sig) != 64 {
		return false, "invalid-param"
	}
	for i := 0; i < len(sig); i++ {
		c := sig[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false, "invalid-param"
		}
	}
	var nheaders []naivePair
	var dateVals []string
	for _, h := range req.Headers {
		nheaders = append(nheaders, naivePair{h.Name, h.Value})
		if strings.EqualFold(h.Name, "x-date") {
			dateVals = append(dateVals, naiveFold(h.Value))
		}
	}
	if len(dateVals) == 0 || strings.Join(dateVals, ",") != strconv.FormatInt(req.TS, 10) {
		return false, "invalid-param"
	}
	var nsigned []string
	for _, s := range req.SignedNames {
		nsigned = append(nsigned, s)
	}
	canonical, ok := naiveCanonical(req.Method, req.Path, nq, nheaders, nsigned, req.PayloadHash)
	if !ok {
		return false, "invalid-param"
	}
	c, exists := db[req.KeyID]
	if !exists {
		return false, "credential-gone"
	}
	moment := now
	if req.Mode == Presigned {
		moment = req.TS
	}
	if c.status == "disabled" || (c.status == "retiring" && moment >= c.validUntil) {
		return false, "credential-stale"
	}
	if req.TS < c.revokeBefore {
		return false, "revoked"
	}
	if req.Mode == Header {
		d := now - req.TS
		if d < 0 {
			d = -d
		}
		if d > 900 {
			return false, "skew"
		}
	} else {
		if req.TS > now+900 {
			return false, "skew"
		}
		if now >= req.TS+req.Expires {
			return false, "expired"
		}
	}
	if req.Region != c.region || req.Service != c.service {
		return false, "scope"
	}
	day := strconv.FormatInt(req.TS/86400, 10)
	scope := day + "/" + req.Region + "/" + req.Service
	ch := sha256.Sum256([]byte(canonical))
	sts := "KS4-HMAC-SHA256\n" + strconv.FormatInt(req.TS, 10) + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	k1 := naiveHMAC([]byte("KS4"+c.secret), day)
	k2 := naiveHMAC(k1, req.Region)
	k3 := naiveHMAC(k2, req.Service)
	k4 := naiveHMAC(k3, "ks4_request")
	expected := hex.EncodeToString(naiveHMAC(k4, sts))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return false, "signature"
	}
	return true, "ok"
}

func reasonFromError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case err == ErrInvalidParam:
		return "invalid-param"
	case err == ErrCredentialGone:
		return "credential-gone"
	case err == ErrCredentialStale:
		return "credential-stale"
	case err == ErrRevoked:
		return "revoked"
	case err == ErrSkew:
		return "skew"
	case err == ErrExpired:
		return "expired"
	case err == ErrScope:
		return "scope"
	case err == ErrSignature:
		return "signature"
	default:
		return "unknown:" + err.Error()
	}
}

const diffAlpha = "abzAZ09-._~/ %+\t\xc3\xa9\xff"

func randBytes(rng *rand.Rand, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = diffAlpha[rng.Intn(len(diffAlpha))]
	}
	return string(b)
}

type scenario struct {
	store *cred.Store
	db    map[string]naiveCred
	req   Request
	now   int64
	why   string
}

func signLocally(c naiveCred, req Request) (Request, bool) {
	r2 := signWithSecret(c.secret, req)
	return r2, true
}

func genScenario(t *testing.T, rng *rand.Rand, idx int) scenario {
	s := cred.NewStore()
	db := map[string]naiveCred{}
	clock := int64(0)

	tenant := []string{"T", "U"}[rng.Intn(2)]
	nKeys := 1 + rng.Intn(3)
	type ki struct {
		id         string
		status     string
		validUntil int64
	}
	var keys []ki
	for i := 0; i < nKeys; i++ {
		id := fmt.Sprintf("k%d-%d", idx, i)
		rot := clock + int64(rng.Intn(60))
		g := int64(1 + rng.Intn(400))
		err := s.Rotate(tenant, id, "sec"+id, "r", "svc", g, rot)
		if err != nil {
			continue
		}
		clock = rot
		for j := range keys {
			if keys[j].status == "active" {
				keys[j].status = "retiring"
				keys[j].validUntil = rot + g
				c := db[keys[j].id]
				c.status = "retiring"
				c.validUntil = rot + g
				db[keys[j].id] = c
			}
		}
		keys = append(keys, ki{id, "active", 0})
		db[id] = naiveCred{tenant: tenant, secret: "sec" + id, region: "r", service: "svc", status: "active"}
	}

	rb := int64(0)
	if rng.Intn(2) == 0 {
		rb = int64(rng.Intn(500))
		if err := s.RevokeBefore(tenant, rb, clock); err == nil {
			for id, c := range db {
				c.revokeBefore = rb
				db[id] = c
			}
		} else {
			rb = 0
		}
	}
	if len(keys) > 0 && rng.Intn(3) == 0 {
		v := keys[rng.Intn(len(keys))]
		if err := s.Disable(v.id, clock); err == nil {
			if c, ok := db[v.id]; ok {
				c.status = "disabled"
				db[v.id] = c
			}
			for j := range keys {
				if keys[j].id == v.id {
					keys[j].status = "disabled"
				}
			}
		}
	}

	keyID := "ghost"
	var chosen naiveCred
	known := rng.Intn(6) != 0
	if known && len(keys) > 0 {
		keyID = keys[rng.Intn(len(keys))].id
		chosen = db[keyID]
	}

	mode := Header
	if rng.Intn(2) == 0 {
		mode = Presigned
	}
	ts := int64(rng.Intn(1000))
	now := ts + int64(rng.Intn(2001)) - 1000
	if now < 0 {
		now = 0
	}
	req := Request{
		KeyID:       keyID,
		Method:      "GET",
		Path:        "/" + randBytes(rng, rng.Intn(4)),
		Region:      "r",
		Service:     "svc",
		Mode:        mode,
		TS:          ts,
		PayloadHash: "UNSIGNED",
	}
	nQ := rng.Intn(3)
	for i := 0; i < nQ; i++ {
		req.Query = append(req.Query, canon.Pair{
			Name: randBytes(rng, 1+rng.Intn(3)), Value: randBytes(rng, rng.Intn(3)),
		})
	}
	req.Headers = []canon.Pair{
		{Name: "Host", Value: randBytes(rng, 1+rng.Intn(6))},
		{Name: "X-Date", Value: fmt.Sprintf("%d", ts)},
	}
	if rng.Intn(3) == 0 {
		req.Headers = append(req.Headers, canon.Pair{Name: "X-Tag", Value: "  " + randBytes(rng, 1+rng.Intn(4)) + " "})
		req.SignedNames = []string{"host", "x-date", "x-tag"}
	} else {
		req.SignedNames = []string{"host", "x-date"}
	}
	if mode == Presigned {
		req.Expires = int64(1 + rng.Intn(1200))
	}

	// 先按完整合法形态签名（变异之前）。
	if known && len(keys) > 0 {
		var ok bool
		req, ok = signLocally(chosen, req)
		if !ok {
			t.Fatal("baseline canonicalization failed")
		}
	}

	why := "baseline"
	if known {
		switch rng.Intn(14) {
		case 0:
			req.Method = "get"
			why = "mut:lowercase-method"
		case 1:
			req.Path = "no-slash"
			why = "mut:relative-path"
		case 2:
			req.SignedNames = []string{"x-date"}
			why = "mut:drop-host-signed"
		case 3:
			req.TS += 5000
			why = "mut:ts-moved-xdate-stays"
		case 4:
			req.Region = "rr"
			why = "mut:scope-region"
		case 5:
			if mode == Presigned {
				req.Query = append(req.Query, canon.Pair{Name: "X-Sig", Value: "ab"})
				why = "mut:double-xsig"
			} else {
				req.Signature = "00"
				why = "mut:short-hex"
			}
		case 6:
			req.KeyID = "ghost"
			why = "mut:unknown-key"
		case 7:
			now = ts + 5000
			why = "mut:far-future-now"
		case 8:
			if mode == Presigned {
				req.Query = append(req.Query, canon.Pair{Name: "X-Expires", Value: "9"})
				why = "mut:duplicate-xexpires-mismatch"
			} else {
				req.Query = append(req.Query, canon.Pair{Name: "X-Sig", Value: "z"})
				why = "mut:header-mode-extra-query(signed-after? no)"
			}
		case 9:
			req.TS = -1
			why = "mut:ts-negative"
		case 10:
			req.Headers[1].Value = fmt.Sprintf("%d", ts+1)
			why = "mut:xdate-mismatch"
		case 11:
			if mode == Presigned {
				req.Expires = 0
				why = "mut:expires-zero"
			} else {
				req.Headers[0].Value = "other.example"
				why = "mut:host-changed"
			}
		case 12:
			req.Service = "other"
			why = "mut:scope-service"
		case 13:
			if mode == Presigned {
				req.PayloadHash = "X"
				why = "mut:presigned-payload"
			} else {
				now = ts - 901
				why = "mut:past-skew-901"
			}
		}
	}

	return scenario{store: s, db: db, req: req, now: now, why: why}
}

func TestDifferential1500(t *testing.T) {
	const total = 1500
	fail := 0
	for seed := int64(0); seed < total; seed++ {
		rng := rand.New(rand.NewSource(seed + 1))
		sc := genScenario(t, rng, int(seed))
		tenant, keyID, err := New(sc.store).Verify(sc.req, sc.now)
		gotReason := reasonFromError(err)
		okNaive, wantReason := naiveVerify(sc.db, sc.req, sc.now)
		pass := (err == nil) == okNaive && gotReason == wantReason
		if !pass {
			fail++
		}
		// 每组都打印输入、输出与判定依据（why 为变异/判定依据），-v 可见。
		{
			t.Logf("seed=%d why=%q mode=%v key=%s ts=%d now=%d exp=%d query=%v -> impl=(%q,%q,%v) naive=(%v,%s)",
				seed, sc.why, sc.req.Mode, sc.req.KeyID, sc.req.TS, sc.now, sc.req.Expires,
				sc.req.Query, tenant, keyID, err, okNaive, wantReason)
		}
		if !pass {
			t.Errorf("seed=%d reason mismatch: impl=%s naive=%s", seed, gotReason, wantReason)
		}
	}
	if fail > 0 {
		t.Fatalf("%d/%d differential cases failed", fail, total)
	}

	// credProbes：每次 Verify 恰好一次 keyID 查表。
	s := cred.NewStore()
	mustRotate(t, s, "T", "k1", "sec", "r", "svc", 100, 0)
	c, _ := s.Snapshot("k1")
	before := s.CredProbes()
	for i := 0; i < 10; i++ {
		req := signReq(t, c, baseReq(Header))
		if _, _, err := New(s).Verify(req, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.CredProbes() - before; got != 10 {
		t.Fatalf("probes delta=%d want 10", got)
	}
}
