package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"ontology/canon"
	"ontology/cred"
)

const (
	maxTS     = 1_000_000_000_000
	skewLimit = 900
	maxExpire = 604_800
)

// Mode 是签名承载方式。
type Mode int

const (
	// Header 表示签名在头部（X-Sig 只是普通查询参数）。
	Header Mode = iota
	// Presigned 表示签名在 X-Sig 查询参数中。
	Presigned
)

var (
	ErrInvalidParam    = errInvalidParam{}
	ErrCredentialGone  = errCredentialGone{}
	ErrCredentialStale = errCredentialStale{}
	ErrRevoked         = errRevoked{}
	ErrSkew            = errSkew{}
	ErrExpired         = errExpired{}
	ErrScope           = errScope{}
	ErrSignature       = errSignature{}
)

type errInvalidParam struct{}

func (errInvalidParam) Error() string { return "verify: invalid parameter" }

type errCredentialGone struct{}

func (errCredentialGone) Error() string { return "verify: credential not found" }

type errCredentialStale struct{}

func (errCredentialStale) Error() string { return "verify: credential unavailable" }

type errRevoked struct{}

func (errRevoked) Error() string { return "verify: signature revoked" }

type errSkew struct{}

func (errSkew) Error() string { return "verify: time skew too large" }

type errExpired struct{}

func (errExpired) Error() string { return "verify: presigned url expired" }

type errScope struct{}

func (errScope) Error() string { return "verify: scope mismatch" }

type errSignature struct{}

func (errSignature) Error() string { return "verify: signature mismatch" }

// Request 是一次待校验请求的全部输入。
type Request struct {
	KeyID       string
	Method      string
	Path        string
	Query       []canon.Pair
	Headers     []canon.Pair
	SignedNames []string
	PayloadHash string
	TS          int64
	Region      string
	Service     string
	Signature   string
	Mode        Mode
	Expires     int64
}

// Verifier 绑定凭证库进行校验。
type Verifier struct{ store *cred.Store }

// New 创建校验器。
func New(store *cred.Store) *Verifier { return &Verifier{store: store} }

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func isLowerHex(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func foldedHeader(headers []canon.Pair, lowerName string) (string, bool) {
	vals := []string(nil)
	for _, h := range headers {
		if strings.EqualFold(h.Name, lowerName) {
			vals = append(vals, canon.FoldHeaderValue(h.Value))
		}
	}
	if vals == nil {
		return "", false
	}
	return strings.Join(vals, ","), true
}

// ComputeSignature 按四层 HMAC 派生签名（供测试侧构造请求复用）。
func ComputeSignature(secret string, ts int64, region, service, stringToSign string) string {
	day := strconv.FormatInt(ts/86400, 10)
	k1 := hmacSHA256([]byte("KS4"+secret), day)
	k2 := hmacSHA256(k1, region)
	k3 := hmacSHA256(k2, service)
	k4 := hmacSHA256(k3, "ks4_request")
	return hex.EncodeToString(hmacSHA256(k4, stringToSign))
}

// Verify 校验请求，成功返回租户与 keyID。
func (v *Verifier) Verify(req Request, now int64) (string, string, error) {
	if req.KeyID == "" || req.Region == "" || req.Service == "" ||
		req.TS < 0 || req.TS > maxTS || now < 0 || now > maxTS ||
		(req.Mode != Header && req.Mode != Presigned) {
		return "", "", ErrInvalidParam
	}

	query := req.Query
	signature := req.Signature
	if req.Mode == Presigned {
		// X-Sig 须恰有一个；规范化前整个剔除。
		sigVals := []string(nil)
		kept := make([]canon.Pair, 0, len(query))
		for _, q := range query {
			if q.Name == "X-Sig" {
				sigVals = append(sigVals, q.Value)
				continue
			}
			kept = append(kept, q)
		}
		if len(sigVals) != 1 {
			return "", "", ErrInvalidParam
		}
		signature = sigVals[0]
		query = kept
		// X-Expires 须存在且每个值都等于 expires 的十进制值。
		wantExp := strconv.FormatInt(req.Expires, 10)
		found := false
		for _, q := range query {
			if q.Name == "X-Expires" {
				found = true
				if q.Value != wantExp {
					return "", "", ErrInvalidParam
				}
			}
		}
		if !found || req.Expires < 1 || req.Expires > maxExpire {
			return "", "", ErrInvalidParam
		}
		if req.PayloadHash != "UNSIGNED" {
			return "", "", ErrInvalidParam
		}
	}
	if !isLowerHex(signature) {
		return "", "", ErrInvalidParam
	}

	// ts 须等于折叠后的 x-date 头。
	dateVal, ok := foldedHeader(req.Headers, "x-date")
	if !ok || dateVal != strconv.FormatInt(req.TS, 10) {
		return "", "", ErrInvalidParam
	}

	// 规范化（canon 的参数非法与缺签名头统一归入参数非法）。
	canonical, err := canon.Build(req.Method, req.Path, query, req.Headers, req.SignedNames, req.PayloadHash)
	if err != nil {
		return "", "", ErrInvalidParam
	}

	// 凭证：按 keyID 恰好一次查表（同一纪元快照）。
	c, ok := v.store.Snapshot(req.KeyID)
	if !ok {
		return "", "", ErrCredentialGone
	}

	// 可用性：已停用；retiring 按动作时刻判定（Header=now，Presigned=ts）。
	moment := now
	if req.Mode == Presigned {
		moment = req.TS
	}
	if c.Status == cred.StatusDisabled ||
		(c.Status == cred.StatusRetiring && moment >= c.ValidUntil) {
		return "", "", ErrCredentialStale
	}

	// 撤销按签名时刻 ts 判定（ts < revokeBefore 无效）。
	if req.TS < c.RevokeBefore {
		return "", "", ErrRevoked
	}

	// 时间类。
	if req.Mode == Header {
		d := now - req.TS
		if d < 0 {
			d = -d
		}
		if d > skewLimit {
			return "", "", ErrSkew
		}
	} else {
		if req.TS > now+skewLimit {
			return "", "", ErrSkew
		}
		if now >= req.TS+req.Expires {
			return "", "", ErrExpired
		}
	}

	// 作用域。
	if req.Region != c.Region || req.Service != c.Service {
		return "", "", ErrScope
	}

	// 签名（恒定时间比较）。
	day := strconv.FormatInt(req.TS/86400, 10)
	scope := day + "/" + req.Region + "/" + req.Service
	canonHash := sha256.Sum256([]byte(canonical))
	sts := "KS4-HMAC-SHA256\n" + strconv.FormatInt(req.TS, 10) + "\n" + scope + "\n" +
		hex.EncodeToString(canonHash[:])
	expected := ComputeSignature(c.Secret, req.TS, req.Region, req.Service, sts)
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return "", "", ErrSignature
	}
	return c.Tenant, c.KeyID, nil
}
