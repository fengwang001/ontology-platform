package dlabel

import "sync/atomic"

// Platform 是基于动态标签的属性级权限平台。
type Platform struct {
	store  *mvccStore
	admins map[string]struct{}
	audit  AuditLog

	evalCount atomic.Int64 // 标签判定求值次数（可观测计数器）
	attrRead  atomic.Int64 // 判定过程中属性读取次数（可观测计数器）

	seq atomic.Int64 // 审计日志序号
}

// Option 是平台构造选项。
type Option func(*Platform)

// WithAuditLog 注入审计日志实现。
func WithAuditLog(l AuditLog) Option {
	return func(p *Platform) { p.audit = l }
}

// WithRetainedVersions 设置可重复读快照保证可用的版本窗口。
func WithRetainedVersions(n int64) Option {
	return func(p *Platform) { p.store = newMVCCStore(n) }
}

// NewPlatform 创建平台。admins 为管理员主体集合：管理员绕过读/写/可见性
// 限制，且只有管理员能变更规则与授权。
func NewPlatform(admins []string, opts ...Option) *Platform {
	p := &Platform{store: newMVCCStore(1000), admins: map[string]struct{}{}}
	for _, a := range admins {
		p.admins[a] = struct{}{}
	}
	for _, o := range opts {
		o(p)
	}
	if p.audit == nil {
		p.audit = NewMemoryAuditLog(0)
	}
	return p
}

func (p *Platform) isAdmin(s string) bool {
	_, ok := p.admins[s]
	return ok
}

// CurrentVersion 返回当前已提交版本（单调递增的逻辑时钟）。
func (p *Platform) CurrentVersion() int64 {
	p.store.commitMu.Lock()
	v := p.store.current.Load()
	p.store.commitMu.Unlock()
	return v
}

// EvalCounters 返回标签求值次数与判定中属性读取次数的累计观测值，
// 用于以不依赖内部实现的方式证明判定开销只随参与判定的属性数量增长。
func (p *Platform) EvalCounters() (tagEvals, attrReads int64) {
	return p.evalCount.Load(), p.attrRead.Load()
}

// RegisterObjectType 登记对象类型及其属性的取值类型（管理员操作）。
func (p *Platform) RegisterObjectType(subject, name string, attrs map[string]ValueKind) error {
	e := p.beginAudit("RegisterObjectType", subject, name, "", formatAttrKinds(attrs))
	var err error
	if !p.isAdmin(subject) {
		err = ErrNotAdmin
	} else {
		p.store.commitMu.Lock()
		p.store.current.Add(1)
		cat := p.store.advanceCatalog()
		am := make(map[string]ValueKind, len(attrs))
		for a, k := range attrs {
			am[a] = k
		}
		cat.types[name] = &objectType{name: name, attrs: am}
		if _, ok := cat.rules[name]; !ok {
			cat.rules[name] = map[string]Expr{}
		}
		p.store.pruneOld()
		p.store.commitMu.Unlock()
	}
	p.finishAudit(e, nil, err)
	return err
}

// CreateInstance 以初始属性值创建实例，整体作为一个新版本原子提交。
func (p *Platform) CreateInstance(subject, ot, id string, values map[string]Value) error {
	e := p.beginAudit("CreateInstance", subject, ot, id, formatValueMap(values))
	err := p.mutate(subject, ot, id, values, true)
	p.finishAudit(e, nil, err)
	return err
}

// WriteInstance 更新实例属性取值，整体作为一个新版本原子提交。
// 标签携带状态从不被存储：本操作只提交属性值，后续读取在同一版本的
// 不可变快照上实时重算标签，因此不可能观察到“值已更新、标签未更新”的中间态。
func (p *Platform) WriteInstance(subject, ot, id string, values map[string]Value) error {
	e := p.beginAudit("WriteInstance", subject, ot, id, formatValueMap(values))
	err := p.mutate(subject, ot, id, values, false)
	p.finishAudit(e, nil, err)
	return err
}

func (p *Platform) mutate(subject, ot, id string, values map[string]Value, create bool) error {
	p.store.commitMu.Lock()
	defer p.store.commitMu.Unlock()

	cat := p.store.currentCatalog()
	obj, ok := cat.types[ot]
	if !ok {
		return ErrUnknownObjectType
	}
	inst := p.store.getInstance(ot, id)
	if create {
		if inst != nil && inst.existsAt(p.store.current.Load()) {
			return &Error{Code: CodeInvalidValue, Msg: "instance already exists: " + id}
		}
	} else if inst == nil || !inst.existsAt(p.store.current.Load()) {
		return ErrUnknownInstance
	}

	for attr, val := range values {
		kind, known := obj.attrs[attr]
		if !known {
			return &Error{Code: CodeUnknownAttribute, Msg: "unknown attribute: " + attr}
		}
		if val.kind != KindNull && val.kind != kind {
			return &Error{Code: CodeInvalidValue, Msg: "value kind mismatch for attribute: " + attr}
		}
	}

	// 非管理员：在候选版本的后状态上做标签判定与可写裁决。
	// 管理员无需授权即可写入。
	if !p.isAdmin(subject) {
		merged := make(map[string]Value, len(obj.attrs))
		for attr := range obj.attrs {
			if inst != nil {
				if v, ok := inst.getAt(attr, p.store.current.Load()); ok {
					merged[attr] = v
				} else {
					merged[attr] = NullValue()
				}
			} else {
				merged[attr] = NullValue()
			}
		}
		for attr, val := range values {
			merged[attr] = val
		}

		eng := newEngine(p, cat, ot, id,
			func(attr string) Value { return merged[attr] },
			true)
		changedAttrs := make([]string, 0, len(values))
		for attr := range values {
			changedAttrs = append(changedAttrs, attr)
		}
		targets := relevantTags(cat, subject, changedAttrs, false)
		tags, _, err := eng.evaluateTargets(targets)
		if err != nil {
			return err
		}
		for attr := range values {
			if d := decideAttr(cat, subject, tags, attr, grantWrite); !d.Allowed {
				return &Error{Code: CodePermissionDenied, Msg: "write denied for attribute: " + attr}
			}
		}
	}

	p.store.current.Add(1)
	d := p.store.loadOrCreateInstance(ot, id)
	for attr, val := range values {
		p.store.appendAttr(d, attr, p.store.current.Load(), val)
	}
	p.store.pruneOld()
	return nil
}

// Snapshot 是一次可重复读范围内固定的多版本快照。
type Snapshot struct {
	platform *Platform
	version  int64
	released bool
}

// Begin 声明一次可重复读范围。其线性化点为持提交锁读取 current 的瞬间：
// 快照版本固定为该瞬间的已提交版本，之后任何并发提交都不影响该快照——
// 这给出了并发写交织下确定且可复现的选取规则。
func (p *Platform) Begin(subject string) *Snapshot {
	e := p.beginAudit("Begin", subject, "", "", "")
	p.store.commitMu.Lock()
	v := p.store.current.Load()
	p.store.commitMu.Unlock()
	p.finishAudit(e, nil, nil)
	return &Snapshot{platform: p, version: v}
}

// Version 返回快照固定的版本号。
func (s *Snapshot) Version() int64 { return s.version }

// Release 释放快照。快照本身不持有任何锁，释放仅使其后续调用明确报错。
func (s *Snapshot) Release() { s.released = true }

func (s *Snapshot) checkUsable() error {
	if s.released {
		return ErrSnapshotReleased
	}
	floor := s.platform.store.current.Load() - s.platform.store.retainVersions
	if s.version < floor {
		return ErrSnapshotUnavailable
	}
	return nil
}
