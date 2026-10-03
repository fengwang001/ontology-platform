package policy

import (
	"errors"
	"sync/atomic"
)

const (
	ActionGet    = "obj:Get"
	ActionList   = "obj:List"
	ActionPut    = "obj:Put"
	ActionDelete = "obj:Delete"
	ActionAll    = "obj:*"

	MaxStatements = 200
)

var (
	ErrTooMany   = errors.New("过多")
	ErrBadPolicy = errors.New("参数非法")
)

type Effect string

const (
	Allow Effect = "Allow"
	Deny  Effect = "Deny"
)

type Statement struct {
	Effect    Effect
	Actions   []string
	Resources []string
}

type BucketStatement struct {
	Statement
	Principals []string
}

func IsAction(a string) bool {
	switch a {
	case ActionGet, ActionList, ActionPut, ActionDelete, ActionAll:
		return true
	}
	return false
}

func IsReadAction(a string) bool  { return a == ActionGet || a == ActionList }
func IsWriteAction(a string) bool { return a == ActionPut || a == ActionDelete }

// ValidateStatement 校验身份策略/边界语句（不含主体模式）。
func ValidateStatement(s Statement) error {
	if s.Effect != Allow && s.Effect != Deny {
		return ErrBadPolicy
	}
	if len(s.Actions) == 0 || len(s.Resources) == 0 {
		return ErrBadPolicy
	}
	for _, a := range s.Actions {
		if !IsAction(a) {
			return ErrBadPolicy
		}
	}
	for _, r := range s.Resources {
		if !ValidateResourcePattern(r) {
			return ErrBadPolicy
		}
	}
	return nil
}

func ValidateBucketStatement(s BucketStatement) error {
	if len(s.Principals) == 0 {
		return ErrBadPolicy
	}
	for _, p := range s.Principals {
		if !ValidatePrincipalPattern(p) {
			return ErrBadPolicy
		}
	}
	return ValidateStatement(s.Statement)
}

// ValidateResourcePattern：* 任意；精确键（允许空串，仅 List 资源会出现空串）；
// 或仅以单个 * 结尾的前缀 x*。
func ValidateResourcePattern(p string) bool {
	for i := 0; i < len(p); i++ {
		if p[i] == '*' && i != len(p)-1 {
			return false
		}
	}
	return true
}

func ValidatePrincipalPattern(p string) bool {
	if p == "*" {
		return true
	}
	if len(p) >= 2 && (p[:2] == "t:" || p[:2] == "p:") {
		return p[2:] != ""
	}
	return false
}

func MatchAction(pattern, action string) bool {
	return pattern == ActionAll || pattern == action
}

func MatchResource(pattern, resource string) bool {
	if pattern == "*" {
		return true
	}
	if n := len(pattern); n > 0 && pattern[n-1] == '*' {
		prefix := pattern[:n-1]
		return len(resource) >= len(prefix) && resource[:len(prefix)] == prefix
	}
	return pattern == resource
}

func MatchPrincipal(pattern, principal, tenant string) bool {
	if pattern == "*" {
		return true
	}
	if len(pattern) >= 2 && pattern[:2] == "t:" {
		return pattern[2:] == tenant
	}
	if len(pattern) >= 2 && pattern[:2] == "p:" {
		return pattern[2:] == principal
	}
	return false
}

// document 是某主体/桶在某纪元下不可变的一份策略。
type document struct {
	stmts []Statement
	index map[string][]*Statement // key：四个具体动作
}

type bucketDocument struct {
	stmts []BucketStatement
	index map[string][]*BucketStatement
}

func newDocument(stmts []Statement) *document {
	d := &document{stmts: stmts, index: map[string][]*Statement{}}
	for i := range stmts {
		s := &stmts[i]
		matched := map[string]bool{}
		for _, a := range s.Actions {
			if a == ActionAll {
				matched[ActionGet] = true
				matched[ActionList] = true
				matched[ActionPut] = true
				matched[ActionDelete] = true
			} else {
				matched[a] = true
			}
		}
		for act := range matched {
			d.index[act] = append(d.index[act], s)
		}
	}
	return d
}

func newBucketDocument(stmts []BucketStatement) *bucketDocument {
	d := &bucketDocument{stmts: stmts, index: map[string][]*BucketStatement{}}
	for i := range stmts {
		s := &stmts[i]
		matched := map[string]bool{}
		for _, a := range s.Actions {
			if a == ActionAll {
				matched[ActionGet] = true
				matched[ActionList] = true
				matched[ActionPut] = true
				matched[ActionDelete] = true
			} else {
				matched[a] = true
			}
		}
		for act := range matched {
			d.index[act] = append(d.index[act], s)
		}
	}
	return d
}

// View 是某纪元下完整策略集合的不可变快照；并发由 gate 的读写锁保证。
type View struct {
	identity map[string]*document
	boundary map[string]*document
	bucket   map[string]*bucketDocument
	probes   *atomic.Uint64
}

// IdentityHits 返回主体身份策略中命中动作与资源的全部语句。
func (v *View) IdentityHits(p, action, resource string) []*Statement {
	return docHits(v.identity[p], action, resource, v.probes)
}

func (v *View) BoundaryHits(p, action, resource string) []*Statement {
	return docHits(v.boundary[p], action, resource, v.probes)
}

func (v *View) BoundarySet(p string) bool {
	_, ok := v.boundary[p]
	return ok
}

func (v *View) BucketHits(bucket, p, tenant, action, resource string) []*BucketStatement {
	d := v.bucket[bucket]
	if d == nil {
		return nil
	}
	var hits []*BucketStatement
	for _, s := range d.index[action] {
		v.probes.Add(1)
		if !principalApplies(s, p, tenant) {
			continue
		}
		if resourceMatches(s.Resources, resource) {
			hits = append(hits, s)
		}
	}
	return hits
}

func docHits(d *document, action, resource string, probes *atomic.Uint64) []*Statement {
	if d == nil {
		return nil
	}
	var hits []*Statement
	for _, s := range d.index[action] {
		probes.Add(1)
		if resourceMatches(s.Resources, resource) {
			hits = append(hits, s)
		}
	}
	return hits
}

func principalApplies(s *BucketStatement, p, tenant string) bool {
	for _, pat := range s.Principals {
		if MatchPrincipal(pat, p, tenant) {
			return true
		}
	}
	return false
}

func resourceMatches(patterns []string, resource string) bool {
	for _, pat := range patterns {
		if MatchResource(pat, resource) {
			return true
		}
	}
	return false
}

type Store struct {
	identity map[string]*document
	boundary map[string]*document
	bucket   map[string]*bucketDocument
	epoch    uint64
	probes   atomic.Uint64
}

func NewStore() *Store {
	return &Store{
		identity: map[string]*document{},
		boundary: map[string]*document{},
		bucket:   map[string]*bucketDocument{},
	}
}

func (s *Store) SetIdentity(p string, stmts []Statement) error {
	for _, st := range stmts {
		if err := ValidateStatement(st); err != nil {
			return err
		}
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	cp := append([]Statement(nil), stmts...)
	s.identity[p] = newDocument(cp)
	s.epoch++
	return nil
}

func (s *Store) SetBoundary(p string, stmts []Statement) error {
	for _, st := range stmts {
		if err := ValidateStatement(st); err != nil {
			return err
		}
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	cp := append([]Statement(nil), stmts...)
	s.boundary[p] = newDocument(cp)
	s.epoch++
	return nil
}

func (s *Store) SetBucket(bucket string, stmts []BucketStatement) error {
	for _, st := range stmts {
		if err := ValidateBucketStatement(st); err != nil {
			return err
		}
	}
	if len(stmts) > MaxStatements {
		return ErrTooMany
	}
	cp := append([]BucketStatement(nil), stmts...)
	s.bucket[bucket] = newBucketDocument(cp)
	s.epoch++
	return nil
}

func (s *Store) Epoch() uint64 { return s.epoch }

// View 返回当前纪元及其不可变快照；纪元与快照取自同一原子步骤。
func (s *Store) View() (uint64, *View) {
	return s.epoch, &View{
		identity: s.identity,
		boundary: s.boundary,
		bucket:   s.bucket,
		probes:   &s.probes,
	}
}

func (s *Store) ResetProbes()       { s.probes.Store(0) }
func (s *Store) ProbeCount() uint64 { return s.probes.Load() }
