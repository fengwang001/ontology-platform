package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology/canon"
	"ontology/cred"
)

func naivePct(s string, keepSlash bool) string {
	const hx = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_' || c == '~' || (keepSlash && c == '/')
		if ok {
			b.WriteByte(c)
		} else {
			b.WriteByte('%')
			b.WriteByte(hx[c>>4])
			b.WriteByte(hx[c&15])
		}
	}
	return b.String()
}

func naiveFold(v string) string {
	return strings.Join(strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' }), " ")
}

func naiveBuild(req Request, query [][2]string) string {
	var qs []string
	for _, p := range query {
		qs = append(qs, naivePct(p[0], false)+"="+naivePct(p[1], false))
	}
	sort.Strings(qs)
	nameSet := map[string]bool{}
	var names []string
	for _, n := range req.SignedNames {
		ln := strings.ToLower(n)
		if !nameSet[ln] {
			nameSet[ln] = true
			names = append(names, ln)
		}
	}
	sort.Strings(names)
	var hb strings.Builder
	for _, n := range names {
		var vals []string
		for _, h := range req.Headers {
			if strings.ToLower(h.Name) == n {
				vals = append(vals, naiveFold(h.Value))
			}
		}
		hb.WriteString(n + ":" + strings.Join(vals, ",") + "\n")
	}
	return req.Method + "\n" + naivePct(req.Path, true) + "\n" + strings.Join(qs, "&") + "\n" +
		hb.String() + strings.Join(names, ";") + "\n" + req.PayloadHash
}

func naiveSign(secret, region, service string, ts int64, canonical string) string {
	mac := func(key, msg []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(msg)
		return m.Sum(nil)
	}
	day := strconv.FormatInt(ts/86400, 10)
	k1 := mac([]byte("KS4"+secret), []byte(day))
	k2 := mac(k1, []byte(region))
	k3 := mac(k2, []byte(service))
	k4 := mac(k3, []byte("ks4_request"))
	sum := sha256.Sum256([]byte(canonical))
	sts := "KS4-HMAC-SHA256\n" + strconv.FormatInt(ts, 10) + "\n" + day + "/" + region + "/" + service + "\n" + hex.EncodeToString(sum[:])
	return hex.EncodeToString(mac(k4, []byte(sts)))
}

type naiveC struct {
	tenant, secret, region, service string
	disabled, retiring              bool
	validUntil                      int64
	revokeT                         int64
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func isUpperASCII(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// naiveVerify 完全按题目次序独立判定，返回 (tenant, 原因)。
func naiveVerify(db map[string]*naiveC, req Request, now int64) (string, string) {
	if req.Mode != Header && req.Mode != Presigned {
		return "", "参数非法"
	}
	if req.TS < 0 || req.TS > 1e12 || now < 0 || now > 1e12 {
		return "", "参数非法"
	}
	if req.KeyID == "" || req.Region == "" || req.Service == "" || req.PayloadHash == "" {
		return "", "参数非法"
	}
	var dv []string
	for _, h := range req.Headers {
		if strings.ToLower(h.Name) == "x-date" {
			dv = append(dv, naiveFold(h.Value))
		}
	}
	if len(dv) == 0 || strings.Join(dv, ",") != strconv.FormatInt(req.TS, 10) {
		return "", "参数非法"
	}
	query := req.Query
	var gotSig string
	if req.Mode == Header {
		if !isHex64(req.Signature) {
			return "", "参数非法"
		}
		gotSig = req.Signature
	} else {
		if req.Expires < 1 || req.Expires > 604800 || req.PayloadHash != "UNSIGNED" {
			return "", "参数非法"
		}
		var sigs, exps []string
		for _, p := range query {
			if p[0] == "X-Sig" {
				sigs = append(sigs, p[1])
			}
			if p[0] == "X-Expires" {
				exps = append(exps, p[1])
			}
		}
		if len(sigs) != 1 || len(exps) != 1 || !isHex64(sigs[0]) || exps[0] != strconv.FormatInt(req.Expires, 10) {
			return "", "参数非法"
		}
		gotSig = sigs[0]
		kept := make([][2]string, 0, len(query)-1)
		removed := false
		for _, p := range query {
			if p[0] == "X-Sig" && !removed {
				removed = true
				continue
			}
			kept = append(kept, p)
		}
		query = kept
	}
	if !isUpperASCII(req.Method) || req.Path == "" || req.Path[0] != '/' {
		return "", "参数非法"
	}
	seen := map[string]bool{}
	hasHost, hasDate := false, false
	for _, n := range req.SignedNames {
		ln := strings.ToLower(n)
		if ln == "host" {
			hasHost = true
		}
		if ln == "x-date" {
			hasDate = true
		}
		seen[ln] = true
	}
	if !hasHost || !hasDate {
		return "", "参数非法"
	}
	for n := range seen {
		present := false
		for _, h := range req.Headers {
			if strings.ToLower(h.Name) == n {
				present = true
			}
		}
		if !present {
			return "", "参数非法"
		}
	}
	c, ok := db[req.KeyID]
	if !ok {
		return "", "凭证不存在"
	}
	if c.disabled {
		return "", "凭证不可用"
	}
	if c.retiring {
		boundary := now
		if req.Mode == Presigned {
			boundary = req.TS
		}
		if boundary >= c.validUntil {
			return "", "凭证不可用"
		}
	}
	if req.TS < c.revokeT {
		return "", "已撤销"
	}
	if req.Mode == Header {
		d := now - req.TS
		if d < 0 {
			d = -d
		}
		if d > 900 {
			return "", "偏差"
		}
	} else {
		if req.TS > now+900 {
			return "", "偏差"
		}
		if now >= req.TS+req.Expires {
			return "", "已过期"
		}
	}
	if req.Region != c.region || req.Service != c.service {
		return "", "作用域不符"
	}
	canonical := naiveBuild(req, query)
	want := naiveSign(c.secret, req.Region, req.Service, req.TS, canonical)
	if !hmac.Equal([]byte(want), []byte(gotSig)) {
		return "", "签名不符"
	}
	return c.tenant, "成功"
}

var randPaths = []string{"/", "/a/b", "/a b/c", "/%d", "/x+y", "/t~z", "/\u00fc", "/q%25r"}

func randValue(r *rand.Rand) string {
	pool := []string{"", "a", "1 2", "x/y", "a%b", "p+q", "z~", "\u00fc", "  x \t y "}
	return pool[r.Intn(len(pool))]
}

func TestNaiveDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rng := rand.New(rand.NewSource(20261003))
	store := cred.NewStore()
	v := New(store)
	db := map[string]*naiveC{}
	var keys []string
	activeByTenant := map[string]string{}
	regions := []string{"r1", "r2"}
	services := []string{"s3", "iam"}

	clock := int64(0)
	for i := 0; i < 10; i++ {
		clock += int64(100 + rng.Intn(500))
		kid := "k" + strconv.Itoa(i)
		tenant := "T" + strconv.Itoa(i%3)
		region := regions[i%2]
		service := services[(i/2)%2]
		if err := store.Rotate(tenant, kid, "secret"+kid, region, service, 600, clock); err != nil {
			t.Fatalf("rotate %s: %v", kid, err)
		}
		if prev := activeByTenant[tenant]; prev != "" {
			db[prev].retiring = true
			db[prev].validUntil = clock + 600
		}
		activeByTenant[tenant] = kid
		db[kid] = &naiveC{tenant: tenant, secret: "secret" + kid, region: region, service: service}
		keys = append(keys, kid)
	}
	// 停用 2 把、对 2 个租户设撤销线，并同步镜像库。
	for _, idx := range []int{0, 3} {
		clock += 10
		kid := keys[idx]
		if err := store.Disable(kid, clock); err != nil {
			t.Fatal(err)
		}
		db[kid].disabled = true
	}
	revTenants := []struct {
		tenant string
		t      int64
	}{{"T0", 800}, {"T1", 1200}}
	for _, rt := range revTenants {
		clock += 10
		if err := store.RevokeBefore(rt.tenant, rt.t, clock); err != nil {
			t.Fatal(err)
		}
		for _, c := range db {
			if c.tenant == rt.tenant && c.revokeT < rt.t {
				c.revokeT = rt.t
			}
		}
	}

	pickKey := func() (string, *naiveC) {
		if rng.Intn(20) == 0 {
			return "ghost-" + strconv.Itoa(rng.Intn(99999)), nil
		}
		kid := keys[rng.Intn(len(keys))]
		return kid, db[kid]
	}

	reasonFromErr := func(err error) string {
		if err == nil {
			return "成功"
		}
		return err.Error()
	}

	for iter := 0; iter < 1500; iter++ {
		keyID, chosen := pickKey()
		ts := int64(rng.Intn(3000))
		now := ts + int64(rng.Intn(2401)) - 1200
		if now < 0 {
			now = 0
		}
		mode := Header
		if rng.Intn(2) == 0 {
			mode = Presigned
		}
		methods := []string{"GET", "POST", "PUT"}
		region := "r1"
		service := "s3"
		if chosen != nil {
			region = chosen.region
			service = chosen.service
		}
		req := Request{
			KeyID: keyID, Method: methods[rng.Intn(3)], Path: randPaths[rng.Intn(len(randPaths))],
			Headers: []canon.Header{
				{Name: "Host", Value: "h.example"},
				{Name: "X-Date", Value: strconv.FormatInt(ts, 10)},
				{Name: "X-Tag", Value: randValue(rng)},
				{Name: "x-tag", Value: randValue(rng)},
			},
			SignedNames: []string{"host", "x-date", "x-tag"},
			PayloadHash: "UNSIGNED",
			TS:          ts,
			Region:      region,
			Service:     service,
			Mode:        mode,
		}
		for i, n := 0, rng.Intn(3); i < n; i++ {
			req.Query = append(req.Query, [2]string{"q" + strconv.Itoa(i), randValue(rng)})
		}
		// 以正确签名起步（keyID 不存在时随便选一把密钥仅用于生成，朴素侧同样查不到）。
		sigC := chosen
		if sigC == nil {
			sigC = db[keys[0]]
		}
		if mode == Presigned {
			req.Expires = int64(1 + rng.Intn(2000))
			base := append([][2]string{}, req.Query...)
			base = append(base, [2]string{"X-Expires", strconv.FormatInt(req.Expires, 10)})
			sig := naiveSign(sigC.secret, req.Region, req.Service, ts, naiveBuild(req, base))
			req.Query = append(base, [2]string{"X-Sig", sig})
		} else {
			req.Signature = naiveSign(sigC.secret, req.Region, req.Service, ts, naiveBuild(req, req.Query))
		}
		// 注入故障。
		switch rng.Intn(14) {
		case 0:
			req.TS++ // x-date 不匹配
		case 1:
			req.Method = "get"
		case 2:
			req.Path = "no-slash"
		case 3:
			req.Region = "other"
		case 4:
			if mode == Header {
				req.Signature = strings.Repeat("0", 64)
			} else {
				for i := range req.Query {
					if req.Query[i][0] == "X-Sig" {
						req.Query[i][1] = strings.Repeat("0", 64)
					}
				}
			}
		case 5:
			now = ts + 5000
		case 6:
			if mode == Presigned {
				now = ts + req.Expires
			}
		case 7:
			req.SignedNames = []string{"host"}
		case 8:
			req.PayloadHash = "HASH"
		case 9:
			req.Expires = 0
		case 10:
			if mode == Presigned {
				req.Query = append(req.Query, [2]string{"X-Sig", strings.Repeat("a", 64)})
			}
		}

		gotTenant, err := v.Verify(req, now)
		wantTenant, wantReason := naiveVerify(db, req, now)
		gotReason := reasonFromErr(err)
		input := fmt.Sprintf("iter=%d mode=%d key=%s ts=%d now=%d expires=%d path=%q method=%q region=%q service=%q",
			iter, req.Mode, req.KeyID, req.TS, now, req.Expires, req.Path, req.Method, req.Region, req.Service)
		if gotReason != wantReason || (err == nil && gotTenant.Tenant != wantTenant) {
			t.Fatalf("差分不一致\n输入: %s\n实现: tenant=%q reason=%q\n朴素: tenant=%q reason=%q",
				input, gotTenant.Tenant, gotReason, wantTenant, wantReason)
		}
		if iter < 15 || iter%150 == 0 {
			t.Logf("输入: %s\n输出: tenant=%q reason=%q；判定依据: %s", input, gotTenant.Tenant, gotReason, gotReason)
		}
	}
	t.Logf("1500 组随机请求与朴素实现判定完全一致（含成功与全部拒绝原因）")
}
