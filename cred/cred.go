package cred

import (
	"errors"
	"sync"
	"sync/atomic"
)

type keyState int

const (
	stateActive keyState = iota
	stateRetiring
	stateDisabled
)

type credential struct {
	keyID      string
	tenant     string
	secret     string
	region     string
	service    string
	state      keyState
	validUntil int64
}

type tenantRec struct {
	revokeT int64
}

// Store 是并发安全的多租户凭证库。
type Store struct {
	mu      sync.RWMutex
	creds   map[string]*credential
	tenants map[string]*tenantRec
	lastNow int64
	epoch   int64
}

// Credential 是凭证库对外暴露的不可变快照。
type Credential struct {
	KeyID      string
	Tenant     string
	Secret     string
	Region     string
	Service    string
	Disabled   bool
	Retiring   bool
	ValidUntil int64
	RevokeT    int64
	Epoch      int64
}

var (
	ErrInvalidArg = errors.New("参数非法")
	ErrClockSkew  = errors.New("时钟回退")
	ErrExists     = errors.New("已存在")
	ErrLimit      = errors.New("超过上限")
	ErrNotFound   = errors.New("不存在")
)

// credProbes 记录 Verify 路径按 keyID 查凭证的次数。
var credProbes atomic.Int64

// NewStore 创建空凭证库。
func NewStore() *Store {
	return &Store{creds: map[string]*credential{}, tenants: map[string]*tenantRec{}}
}

func nonEmpty(s string) bool { return s != "" }

func inRange(v, lo, hi int64) bool { return v >= lo && v <= hi }

// Rotate 轮换租户凭证。
func (s *Store) Rotate(tenant, keyID, secret, region, service string, g, now int64) error {
	if tenant == "" || !nonEmpty(keyID) || !nonEmpty(secret) || !nonEmpty(region) || !nonEmpty(service) ||
		!inRange(g, 1, 10_000_000) || !inRange(now, 0, 1_000_000_000_000) {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockSkew
	}
	if _, ok := s.creds[keyID]; ok {
		return ErrExists
	}
	signable := 0
	for _, c := range s.creds {
		if c.tenant != tenant {
			continue
		}
		if c.state == stateActive || (c.state == stateRetiring && now < c.validUntil) {
			signable++
		}
	}
	if signable >= 2 {
		return ErrLimit
	}
	t := s.tenants[tenant]
	if t == nil {
		t = &tenantRec{}
		s.tenants[tenant] = t
	}
	for _, c := range s.creds {
		if c.tenant == tenant && c.state == stateActive {
			c.state = stateRetiring
			c.validUntil = now + g
		}
	}
	s.creds[keyID] = &credential{
		keyID:   keyID,
		tenant:  tenant,
		secret:  secret,
		region:  region,
		service: service,
		state:   stateActive,
	}
	s.lastNow = now
	s.epoch++
	return nil
}

// Disable 立即停用凭证。
func (s *Store) Disable(keyID string, now int64) error {
	if !nonEmpty(keyID) || !inRange(now, 0, 1_000_000_000_000) {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockSkew
	}
	c := s.creds[keyID]
	if c == nil {
		return ErrNotFound
	}
	s.lastNow = now
	if c.state != stateDisabled {
		c.state = stateDisabled
		s.epoch++
	}
	return nil
}

// RevokeBefore 撤销租户内 ts 严格小于 t 的签名。
func (s *Store) RevokeBefore(tenant string, t, now int64) error {
	if tenant == "" || !inRange(t, 0, 1_000_000_000_000) || !inRange(now, 0, 1_000_000_000_000) {
		return ErrInvalidArg
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < s.lastNow {
		return ErrClockSkew
	}
	ten := s.tenants[tenant]
	hasCred := false
	for _, c := range s.creds {
		if c.tenant == tenant {
			hasCred = true
			break
		}
	}
	if ten == nil && !hasCred {
		return ErrNotFound
	}
	if ten == nil {
		ten = &tenantRec{}
		s.tenants[tenant] = ten
	}
	s.lastNow = now
	if t > ten.revokeT {
		ten.revokeT = t
		s.epoch++
	}
	return nil
}

// Lookup 按 keyID 取一次凭证快照；不存在时 ok 为 false。
func (s *Store) Lookup(keyID string) (Credential, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	credProbes.Add(1)
	c := s.creds[keyID]
	if c == nil {
		return Credential{}, false
	}
	var revokeT int64
	if ten := s.tenants[c.tenant]; ten != nil {
		revokeT = ten.revokeT
	}
	return Credential{
		KeyID:      c.keyID,
		Tenant:     c.tenant,
		Secret:     c.secret,
		Region:     c.region,
		Service:    c.service,
		Disabled:   c.state == stateDisabled,
		Retiring:   c.state == stateRetiring,
		ValidUntil: c.validUntil,
		RevokeT:    revokeT,
		Epoch:      s.epoch,
	}, true
}
