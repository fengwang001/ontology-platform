package gate

import (
	"sync"

	"ontology/policy"
	"ontology/tenant"
)

// 错误别名：调用方只需导入 gate 包即可判别错误类别。
var (
	ErrNotFound          = tenant.ErrNotFound
	ErrAlreadyExists     = tenant.ErrDuplicate
	ErrBadRequest        = tenant.ErrBadRequest
	ErrBadPolicy         = policy.ErrBadPolicy
	ErrTooManyStatements = policy.ErrTooMany
	ErrTenantNotFound    = tenant.ErrNotFound
)

type Reason string

const (
	ReasonOK         Reason = "允许"
	ReasonBadRequest Reason = "参数非法"
	ReasonNoBucket   Reason = "桶不存在"
	ReasonTenantSusp Reason = "租户已暂停"
	ReasonFrozen     Reason = "桶被冻结"
	ReasonDeny       Reason = "命中 Deny"
	ReasonOutOfBound Reason = "越出边界"
	ReasonNoPermit   Reason = "无许可"
	ReasonNoIdentity Reason = "缺身份许可"
	ReasonNoResource Reason = "缺资源许可"
)

type Decision struct {
	Allowed bool
	Reason  Reason
	Epoch   uint64
}

type Gate struct {
	mu sync.RWMutex

	reg   *tenant.Registry
	store *policy.Store
}

func New() *Gate {
	return &Gate{
		reg:   tenant.New(),
		store: policy.NewStore(),
	}
}

func (g *Gate) RegisterPrincipal(p, t string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reg.RegisterPrincipal(p, t)
}

func (g *Gate) CreateBucket(owner, bucket string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reg.CreateBucket(owner, bucket)
}

func (g *Gate) Suspend(t string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reg.Suspend(t)
}

func (g *Gate) Resume(t string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reg.Resume(t)
}

func (g *Gate) SetIdentityPolicy(p string, stmts []policy.Statement) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.principalKnown(p) {
		return tenant.ErrNotFound
	}
	return g.store.SetIdentity(p, stmts)
}

func (g *Gate) SetBoundary(p string, stmts []policy.Statement) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.principalKnown(p) {
		return tenant.ErrNotFound
	}
	return g.store.SetBoundary(p, stmts)
}

func (g *Gate) SetBucketPolicy(bucket string, stmts []policy.BucketStatement) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.bucketKnown(bucket) {
		return tenant.ErrNotFound
	}
	return g.store.SetBucket(bucket, stmts)
}

func (g *Gate) principalKnown(p string) bool {
	if p == "" {
		return false
	}
	snap := g.reg.Snapshot()
	_, ok := snap.PrincipalTenant[p]
	return ok
}

func (g *Gate) bucketKnown(bucket string) bool {
	if bucket == "" {
		return false
	}
	snap := g.reg.Snapshot()
	_, ok := snap.BucketOwner[bucket]
	return ok
}

func (g *Gate) Epoch() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.store.Epoch()
}

func (g *Gate) ResetProbes() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store.ResetProbes()
}

func (g *Gate) ProbeCount() uint64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.store.ProbeCount()
}

// Check 按固定次序判定，只报第一个拒绝原因；全程使用同一纪元的注册表与策略快照。
func (g *Gate) Check(p, action, bucket, resource string) Decision {
	g.mu.RLock()
	defer g.mu.RUnlock()
	reg := g.reg.Snapshot()
	epoch, view := g.store.View()

	deny := func(r Reason) Decision { return Decision{false, r, epoch} }

	if !policy.IsAction(action) {
		return deny(ReasonBadRequest)
	}
	if action != policy.ActionList && resource == "" {
		return deny(ReasonBadRequest)
	}
	pt, ok := reg.PrincipalTenant[p]
	if !ok {
		return deny(ReasonBadRequest)
	}
	owner, ok := reg.BucketOwner[bucket]
	if !ok {
		return deny(ReasonNoBucket)
	}

	if reg.Suspended[pt] {
		return deny(ReasonTenantSusp)
	}
	if reg.Suspended[owner] && pt != owner && policy.IsWriteAction(action) {
		return deny(ReasonFrozen)
	}

	idHits := view.IdentityHits(p, action, resource)
	bdHits := view.BoundaryHits(p, action, resource)
	bkHits := view.BucketHits(bucket, p, pt, action, resource)

	if hasEffect(idHits, policy.Deny) || hasEffect(bdHits, policy.Deny) ||
		bucketHasEffect(bkHits, policy.Deny) {
		return deny(ReasonDeny)
	}

	if view.BoundarySet(p) && !hasEffect(bdHits, policy.Allow) {
		return deny(ReasonOutOfBound)
	}

	idAllow := hasEffect(idHits, policy.Allow)
	bkAllow := bucketHasEffect(bkHits, policy.Allow)

	if pt == owner {
		if !idAllow && !bkAllow {
			return deny(ReasonNoPermit)
		}
	} else {
		if !idAllow {
			return deny(ReasonNoIdentity)
		}
		if !bkAllow {
			return deny(ReasonNoResource)
		}
	}

	return Decision{true, ReasonOK, epoch}
}

func hasEffect(hits []*policy.Statement, e policy.Effect) bool {
	for _, s := range hits {
		if s.Effect == e {
			return true
		}
	}
	return false
}

func bucketHasEffect(hits []*policy.BucketStatement, e policy.Effect) bool {
	for _, s := range hits {
		if s.Effect == e {
			return true
		}
	}
	return false
}
