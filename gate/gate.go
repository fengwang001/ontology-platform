package gate

import (
	"ontology/policy"
	"ontology/tenant"
	"sync"
)

// 拒绝原因；空串表示允许。
const (
	ReasonInvalid       = "参数非法"
	ReasonNoBucket      = "桶不存在"
	ReasonTenantPaused  = "租户已暂停"
	ReasonBucketFrozen  = "桶被冻结"
	ReasonDeny          = "命中 Deny"
	ReasonOutOfBoundary = "越出边界"
	ReasonNoPermission  = "无许可"
	ReasonNoIdentity    = "缺身份许可"
	ReasonNoResource    = "缺资源许可"
)

// Decision 是一次 Check 的结果。
type Decision struct {
	Allowed bool
	Reason  string
	Epoch   int
	// Basis 记录判定依据：各来源首个命中的 Allow/Deny，供日志复现。
	Basis string
}

type resolver struct{ reg *tenant.Registry }

func (r resolver) PrincipalExists(p string) bool {
	_, ok := r.reg.TenantOf(p)
	return ok
}

func (r resolver) BucketExists(bucket string) bool {
	_, ok := r.reg.BucketOwner(bucket)
	return ok
}

// Gateway 组合租户登记与策略存储，是跨租户访问判定的统一入口。
type Gateway struct {
	Reg    *tenant.Registry
	Policy *policy.Store

	mu         sync.Mutex
	lastProbes int
}

// LastProbes 返回最近一次 Check 扫描到的、动作模式能匹配所查动作的语句条数。
func (g *Gateway) LastProbes() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastProbes
}

// New 创建空网关。
func New() *Gateway {
	reg := tenant.NewRegistry()
	return &Gateway{Reg: reg, Policy: policy.NewStore(resolver{reg})}
}

func (g *Gateway) RegisterPrincipal(p, t string) error {
	return g.Reg.RegisterPrincipal(p, t)
}

func (g *Gateway) CreateBucket(owner, bucket string) error {
	return g.Reg.CreateBucket(owner, bucket)
}

func (g *Gateway) Suspend(t string) error { return g.Reg.Suspend(t) }
func (g *Gateway) Resume(t string) error  { return g.Reg.Resume(t) }

func (g *Gateway) SetIdentityPolicy(p string, stmts []policy.Statement) error {
	return g.Policy.SetIdentityPolicy(p, stmts)
}

func (g *Gateway) SetBoundary(p string, stmts []policy.Statement) error {
	return g.Policy.SetBoundary(p, stmts)
}

func (g *Gateway) SetBucketPolicy(bucket string, stmts []policy.BucketStatement) error {
	return g.Policy.SetBucketPolicy(bucket, stmts)
}

// Check 按固定次序只报第一个原因；返回结果携带判定所用纪元。
func (g *Gateway) Check(p, action, bucket, resource string) Decision {
	// 1. 参数非法：动作非法、非 List 资源为空、主体未登记。
	if !policy.ValidAction(action) || resource == "" && action != policy.ActionList {
		return Decision{Reason: ReasonInvalid, Basis: "动作或资源参数非法"}
	}
	t, ok := g.Reg.TenantOf(p)
	if !ok {
		return Decision{Reason: ReasonInvalid, Basis: "主体未登记"}
	}
	// 2. 桶不存在。
	owner, ok := g.Reg.BucketOwner(bucket)
	if !ok {
		return Decision{Reason: ReasonNoBucket, Basis: "桶 " + bucket + " 不存在"}
	}
	// 以下判定都基于同一纪元的完整策略快照。
	ev, epoch := g.Policy.Snapshot(action)

	// 3. 请求方租户已暂停。
	if g.Reg.IsSuspended(t) {
		return Decision{Reason: ReasonTenantPaused, Epoch: epoch, Basis: "租户 " + t + " 已暂停"}
	}
	// 4. 桶属主租户已暂停且为写：只读冻结。
	if g.Reg.IsSuspended(owner) && policy.IsWrite(action) {
		return Decision{Reason: ReasonBucketFrozen, Epoch: epoch, Basis: "属主租户 " + owner + " 已暂停，写被冻结"}
	}

	idEntries := ev.Identity(p)
	bktEntries := ev.Bucket(bucket)
	bndEntries, bndSet := ev.Boundary(p)

	g.mu.Lock()
	g.lastProbes = ev.Probes()
	g.mu.Unlock()

	// 5. 任一份适用策略有命中 Deny（身份、边界、适用于 p 的桶语句）。
	for _, en := range idEntries {
		if en.Effect() == policy.EffectDeny && en.Hit(resource) {
			return deny(epoch, "身份策略 Deny")
		}
	}
	for _, en := range bndEntries {
		if en.Effect() == policy.EffectDeny && en.Hit(resource) {
			return deny(epoch, "权限边界 Deny")
		}
	}
	for _, en := range bktEntries {
		if en.Effect() == policy.EffectDeny && en.HitBucket(p, t, resource) {
			return deny(epoch, "桶策略 Deny")
		}
	}

	// 6. 有边界而边界无命中 Allow：越出边界。
	boundaryAllow := false
	if bndSet {
		for _, en := range bndEntries {
			if en.Effect() == policy.EffectAllow && en.Hit(resource) {
				boundaryAllow = true
				break
			}
		}
		if !boundaryAllow {
			return Decision{Reason: ReasonOutOfBoundary, Epoch: epoch,
				Basis: "边界已设置但无 Allow 命中"}
		}
	}

	// 7. 基础许可。
	idAllow := false
	for _, en := range idEntries {
		if en.Effect() == policy.EffectAllow && en.Hit(resource) {
			idAllow = true
			break
		}
	}
	bktAllow := false
	for _, en := range bktEntries {
		if en.Effect() == policy.EffectAllow && en.HitBucket(p, t, resource) {
			bktAllow = true
			break
		}
	}

	if t == owner {
		// 同租户：身份或桶策略其一即可（并集）。
		if idAllow || bktAllow {
			return Decision{Allowed: true, Epoch: epoch, Basis: sameTenantBasis(idAllow, bktAllow)}
		}
		return Decision{Reason: ReasonNoPermission, Epoch: epoch,
			Basis: "同租户且身份/桶策略均无 Allow"}
	}
	// 跨租户：身份 Allow 且桶策略 Allow（交集）。
	if !idAllow {
		return Decision{Reason: ReasonNoIdentity, Epoch: epoch, Basis: "跨租户缺身份策略 Allow"}
	}
	if !bktAllow {
		return Decision{Reason: ReasonNoResource, Epoch: epoch, Basis: "跨租户缺桶策略 Allow"}
	}
	return Decision{Allowed: true, Epoch: epoch, Basis: "跨租户身份+桶策略 Allow 交集"}
}

func deny(epoch int, basis string) Decision {
	return Decision{Reason: ReasonDeny, Epoch: epoch, Basis: basis}
}

func sameTenantBasis(idAllow, bktAllow bool) string {
	switch {
	case idAllow && bktAllow:
		return "同租户身份+桶策略 Allow 并集"
	case idAllow:
		return "同租户身份策略 Allow"
	default:
		return "同租户桶策略 Allow"
	}
}
