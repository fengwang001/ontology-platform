package verify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"ontology/canon"
	"ontology/cred"
)

// Mode 为签名承载模式。
type Mode int

const (
	Header Mode = iota
	Presigned
)

// Request 是待校验请求。
type Request struct {
	KeyID       string
	Method      string
	Path        string
	Query       [][2]string
	Headers     []canon.Header
	SignedNames []string
	PayloadHash string
	TS          int64
	Region      string
	Service     string
	Signature   string
	Mode        Mode
	Expires     int64
}

// Result 是校验成功的结果。
type Result struct {
	Tenant string
	KeyID  string
}

var (
	ErrInvalidArg         = errors.New("参数非法")
	ErrCredentialMissing  = errors.New("凭证不存在")
	ErrCredentialUnusable = errors.New("凭证不可用")
	ErrRevoked            = errors.New("已撤销")
	ErrSkew               = errors.New("偏差")
	ErrExpired            = errors.New("已过期")
	ErrScope              = errors.New("作用域不符")
	ErrSignature          = errors.New("签名不符")
)

// Verifier 绑定一个凭证库进行签名校验。
type Verifier struct {
	store *cred.Store
}

// New 创建 Verifier。
func New(store *cred.Store) *Verifier {
	return &Verifier{store: store}
}

func lower(s string) string { return strings.ToLower(s) }

func foldValue(v string) string {
	fields := strings.FieldsFunc(v, func(r rune) bool { return r == ' ' || r == '\t' })
	return strings.Join(fields, " ")
}

func headerValue(headers []canon.Header, name string) (string, bool) {
	parts := make([]string, 0, 1)
	found := false
	for _, h := range headers {
		if lower(h.Name) == name {
			parts = append(parts, foldValue(h.Value))
			found = true
		}
	}
	return strings.Join(parts, ","), found
}

func isLowerHex64(s string) bool {
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

func sign(secret, region, service string, ts int64, canonical string) string {
	day := strconv.FormatInt(ts/86400, 10)
	k1 := hmacSHA256([]byte("KS4"+secret), []byte(day))
	k2 := hmacSHA256(k1, []byte(region))
	k3 := hmacSHA256(k2, []byte(service))
	k4 := hmacSHA256(k3, []byte("ks4_request"))
	scope := day + "/" + region + "/" + service
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := "KS4-HMAC-SHA256\n" + strconv.FormatInt(ts, 10) + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	return hex.EncodeToString(hmacSHA256(k4, []byte(stringToSign)))
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}

func validateAndBuild(req Request, now int64) (canonical, expectedSig string, lookupKey string, invalid bool) {
	if req.Mode != Header && req.Mode != Presigned {
		return "", "", "", true
	}
	if req.TS < 0 || req.TS > 1_000_000_000_000 || now < 0 || now > 1_000_000_000_000 {
		return "", "", "", true
	}
	if req.KeyID == "" || req.Region == "" || req.Service == "" || req.PayloadHash == "" {
		return "", "", "", true
	}
	dateVal, ok := headerValue(req.Headers, "x-date")
	if !ok || dateVal != strconv.FormatInt(req.TS, 10) {
		return "", "", "", true
	}
	query := req.Query
	if req.Mode == Header {
		if !isLowerHex64(req.Signature) {
			return "", "", "", true
		}
		expectedSig = req.Signature
	} else {
		if req.Expires < 1 || req.Expires > 604800 || req.PayloadHash != "UNSIGNED" {
			return "", "", "", true
		}
		sigIdx := make([]int, 0, 1)
		expIdx := make([]int, 0, 1)
		for i, p := range query {
			switch p[0] {
			case "X-Sig":
				sigIdx = append(sigIdx, i)
			case "X-Expires":
				expIdx = append(expIdx, i)
			}
		}
		if len(sigIdx) != 1 || len(expIdx) != 1 {
			return "", "", "", true
		}
		sigVal := query[sigIdx[0]][1]
		if !isLowerHex64(sigVal) || query[expIdx[0]][1] != strconv.FormatInt(req.Expires, 10) {
			return "", "", "", true
		}
		expectedSig = sigVal
		kept := make([][2]string, 0, len(query)-1)
		for i, p := range query {
			if i != sigIdx[0] {
				kept = append(kept, p)
			}
		}
		query = kept
	}
	c, err := canon.Build(req.Method, req.Path, query, req.Headers, req.SignedNames, req.PayloadHash)
	if err != nil {
		return "", "", "", true
	}
	return c, expectedSig, req.KeyID, false
}

// Verify 校验请求，成功返回租户与 keyID。
func (v *Verifier) Verify(req Request, now int64) (Result, error) {
	canonical, gotSig, keyID, invalid := validateAndBuild(req, now)
	if invalid {
		return Result{}, ErrInvalidArg
	}
	cred0, ok := v.store.Lookup(keyID)
	if !ok {
		return Result{}, ErrCredentialMissing
	}
	if cred0.Disabled {
		return Result{}, ErrCredentialUnusable
	}
	if cred0.Retiring {
		boundary := now
		if req.Mode == Presigned {
			boundary = req.TS
		}
		if boundary >= cred0.ValidUntil {
			return Result{}, ErrCredentialUnusable
		}
	}
	if req.TS < cred0.RevokeT {
		return Result{}, ErrRevoked
	}
	if req.Mode == Header {
		d := now - req.TS
		if d < 0 {
			d = -d
		}
		if d > 900 {
			return Result{}, ErrSkew
		}
	} else {
		if req.TS > now+900 {
			return Result{}, ErrSkew
		}
		if now >= req.TS+req.Expires {
			return Result{}, ErrExpired
		}
	}
	if req.Region != cred0.Region || req.Service != cred0.Service {
		return Result{}, ErrScope
	}
	wantSig := sign(cred0.Secret, req.Region, req.Service, req.TS, canonical)
	if !hmac.Equal([]byte(wantSig), []byte(gotSig)) {
		return Result{}, ErrSignature
	}
	return Result{Tenant: cred0.Tenant, KeyID: cred0.KeyID}, nil
}
