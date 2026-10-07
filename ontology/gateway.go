package ontology

import "sync"

// DefaultMaxPropagationDepth 是平台声明的传播深度上限。
const DefaultMaxPropagationDepth = 16

// Config 是网关配置。
type Config struct {
	// MaxPropagationDepth 为传播深度上限，超过即 ErrInvalidDepth。
	// 零值时使用 DefaultMaxPropagationDepth。
	MaxPropagationDepth int
}

func (c Config) maxDepth() int {
	if c.MaxPropagationDepth <= 0 {
		return DefaultMaxPropagationDepth
	}
	return c.MaxPropagationDepth
}

// Gateway 是权限沿链接类型图传播与覆盖的判定网关。
//
// 并发语义：所有导出方法都可在任意 goroutine 中并发调用；内部以
// 单一互斥锁串行化全部读写，因此并发调用的最终可观察结果必然等价
// 于某个全局串行顺序，任一时刻的判定结果对应该顺序下的某个确定
// 前缀。被拒绝的操作在提交前完成全部校验，不改变任何既有状态。
type Gateway struct {
	mu       sync.RWMutex
	maxDepth int

	objectTypes map[ObjectTypeID]struct{}
	linkTypes   map[LinkTypeID]LinkType
	overrides   map[ObjectTypeID]OverrideMode
	// grants[subject][objectType][action] = allow/deny
	grants map[SubjectID]map[ObjectTypeID]map[Action]grantValue
}

// NewGateway 创建一个空网关。
func NewGateway(cfg Config) *Gateway {
	return &Gateway{
		maxDepth:    cfg.maxDepth(),
		objectTypes: make(map[ObjectTypeID]struct{}),
		linkTypes:   make(map[LinkTypeID]LinkType),
		overrides:   make(map[ObjectTypeID]OverrideMode),
		grants:      make(map[SubjectID]map[ObjectTypeID]map[Action]grantValue),
	}
}

// AddObjectType 注册一个对象类型；重复注册是幂等的。
func (g *Gateway) AddObjectType(id ObjectTypeID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.objectTypes[id] = struct{}{}
}

// RemoveObjectType 移除对象类型，并级联移除其覆盖规则、相关授权
// 与两端挂在该类型上的链接类型。
func (g *Gateway) RemoveObjectType(id ObjectTypeID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.objectTypes, id)
	delete(g.overrides, id)
	for subj, m := range g.grants {
		delete(m, id)
		if len(m) == 0 {
			delete(g.grants, subj)
		}
	}
	for id2, l := range g.linkTypes {
		if l.From == id || l.To == id {
			delete(g.linkTypes, id2)
		}
	}
}

// AddLinkType 新增链接类型。校验失败（类型不存在、深度非法、
// 引入传播环路）时返回错误且不改变任何既有状态。
func (g *Gateway) AddLinkType(lt LinkType) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[lt.ID]; ok {
		return ErrLinkTypeExists
	}
	if err := g.validateLinkType(lt); err != nil {
		return err
	}
	g.linkTypes[lt.ID] = lt
	if g.hasPropagationCycle() {
		delete(g.linkTypes, lt.ID)
		return ErrPropagationCycle
	}
	return nil
}

// UpdateLinkType 更新既有链接类型；校验与 AddLinkType 相同，
// 失败时既有配置保持不变。
func (g *Gateway) UpdateLinkType(lt LinkType) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	old, ok := g.linkTypes[lt.ID]
	if !ok {
		return ErrLinkTypeNotFound
	}
	if err := g.validateLinkType(lt); err != nil {
		return err
	}
	g.linkTypes[lt.ID] = lt
	if g.hasPropagationCycle() {
		g.linkTypes[lt.ID] = old
		return ErrPropagationCycle
	}
	return nil
}

// RemoveLinkType 移除链接类型。
func (g *Gateway) RemoveLinkType(id LinkTypeID) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[id]; !ok {
		return ErrLinkTypeNotFound
	}
	delete(g.linkTypes, id)
	return nil
}

// validateLinkType 校验链接类型引用的对象类型存在且深度合法。
func (g *Gateway) validateLinkType(lt LinkType) error {
	if _, ok := g.objectTypes[lt.From]; !ok {
		return ErrObjectTypeNotFound
	}
	if _, ok := g.objectTypes[lt.To]; !ok {
		return ErrObjectTypeNotFound
	}
	if lt.MaxDepth < 0 || lt.MaxDepth > g.maxDepth {
		return ErrInvalidDepth
	}
	return nil
}

// hasPropagationCycle 在全部参与传播的链接类型构成的有向图上
// 做三色 DFS 环路检测。调用方须已持有锁。
func (g *Gateway) hasPropagationCycle() bool {
	adj := make(map[ObjectTypeID][]ObjectTypeID)
	for _, l := range g.linkTypes {
		if l.participates() {
			adj[l.From] = append(adj[l.From], l.To)
		}
	}
	return hasCycle(adj)
}

// hasCycle 是有向图三色 DFS 环路检测的独立实现。
func hasCycle(adj map[ObjectTypeID][]ObjectTypeID) bool {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[ObjectTypeID]int)
	var visit func(u ObjectTypeID) bool
	visit = func(u ObjectTypeID) bool {
		color[u] = gray
		for _, v := range adj[u] {
			switch color[v] {
			case gray:
				return true
			case white:
				if visit(v) {
					return true
				}
			}
		}
		color[u] = black
		return false
	}
	for u := range adj {
		if color[u] == white && visit(u) {
			return true
		}
	}
	return false
}

// SetOverride 设置（或替换）目标对象类型的覆盖规则；
// OverrideNone 表示清除。一个类型在任意时刻只有一条生效规则，
// 因此该操作不可能产生两种覆盖并存的矛盾状态。
func (g *Gateway) SetOverride(typ ObjectTypeID, mode OverrideMode) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[typ]; !ok {
		return ErrObjectTypeNotFound
	}
	if mode == OverrideNone {
		delete(g.overrides, typ)
	} else {
		g.overrides[typ] = mode
	}
	return nil
}

// AddOverrideRule 以“追加”语义声明覆盖规则：若该类型已存在
// 不同形态的规则，则两种覆盖不可调和，返回
// ErrConflictingOverrides 且不改变既有规则。
func (g *Gateway) AddOverrideRule(typ ObjectTypeID, mode OverrideMode) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[typ]; !ok {
		return ErrObjectTypeNotFound
	}
	if mode == OverrideNone {
		return nil
	}
	if cur, ok := g.overrides[typ]; ok && cur != mode {
		return ErrConflictingOverrides
	}
	g.overrides[typ] = mode
	return nil
}

// Grant 记录主体对某对象类型某动作的显式允许。
func (g *Gateway) Grant(subject SubjectID, typ ObjectTypeID, action Action) error {
	return g.setGrant(subject, typ, action, grantAllow)
}

// Deny 记录主体对某对象类型某动作的显式否定项。显式否定始终
// 优先于任何经传播得到的权限。
func (g *Gateway) Deny(subject SubjectID, typ ObjectTypeID, action Action) error {
	return g.setGrant(subject, typ, action, grantDeny)
}

// Revoke 撤销主体对某对象类型某动作的授权记录（允许或否定）。
func (g *Gateway) Revoke(subject SubjectID, typ ObjectTypeID, action Action) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[typ]; !ok {
		return ErrObjectTypeNotFound
	}
	if m := g.grants[subject]; m != nil {
		if actions := m[typ]; actions != nil {
			delete(actions, action)
			if len(actions) == 0 {
				delete(m, typ)
			}
		}
		if len(m) == 0 {
			delete(g.grants, subject)
		}
	}
	return nil
}

func (g *Gateway) setGrant(subject SubjectID, typ ObjectTypeID, action Action, v grantValue) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[typ]; !ok {
		return ErrObjectTypeNotFound
	}
	m := g.grants[subject]
	if m == nil {
		m = make(map[ObjectTypeID]map[Action]grantValue)
		g.grants[subject] = m
	}
	actions := m[typ]
	if actions == nil {
		actions = make(map[Action]grantValue)
		m[typ] = actions
	}
	actions[action] = v
	return nil
}
