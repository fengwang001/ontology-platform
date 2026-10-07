package ontology

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
)

// 变更 API 返回的哨兵错误。
var (
	ErrUnknownObjectType = errors.New("ontology: unknown object type")
	ErrUnknownLinkType   = errors.New("ontology: unknown link type")
	ErrUnknownTag        = errors.New("ontology: unknown tag")
	ErrUnknownRole       = errors.New("ontology: unknown role")
	ErrUnknownSubject    = errors.New("ontology: unknown subject")
	ErrUnknownInstance   = errors.New("ontology: unknown instance")
	ErrDuplicate         = errors.New("ontology: duplicate declaration")
)

// declarations 是全部原始声明，是系统的唯一事实来源。
type declarations struct {
	objectTypes  map[string]bool
	linkTypes    map[string]bool
	tags         map[string]bool
	edges        map[LinkEdge]bool
	attachments  map[Attachment]bool
	propagations map[Propagation]bool
	blocks       map[Block]bool
	roleParents  map[string]map[string]bool // role -> parent roles
	grants       map[Grant]bool
	subjectRoles map[string]map[string]bool // subject -> roles
	instances    map[string]string          // instance -> object type
}

func newDeclarations() *declarations {
	return &declarations{
		objectTypes:  map[string]bool{},
		linkTypes:    map[string]bool{},
		tags:         map[string]bool{},
		edges:        map[LinkEdge]bool{},
		attachments:  map[Attachment]bool{},
		propagations: map[Propagation]bool{},
		blocks:       map[Block]bool{},
		roleParents:  map[string]map[string]bool{},
		grants:       map[Grant]bool{},
		subjectRoles: map[string]map[string]bool{},
		instances:    map[string]string{},
	}
}

// snapshot 是由声明全量重算出的物化视图。每次变更后整体原子替换，
// 因此增量维护的结果与从空白状态重建的结果必然一致。
type snapshot struct {
	// carried[objectType][tag] = 该对象类型携带该标签的全部来源。
	carried map[string]map[string][]TagSource
	// shadow[objectType][tag]：忽略阻断点时该标签是否可达，
	// 用于区分“全部路径被阻断”与“标签本就不会到达”。
	shadow map[string]map[string]bool
	// grantsByRole[role] = 该角色直接声明的全部授权（按标签、效果排序），
	// 使单次判定的授权查找只涉及实际遍历到的角色节点。
	grantsByRole map[string][]Grant
}

// Engine 是访问控制引擎。全部并发调用通过一把读写锁串行化为
// 某个串行顺序：写操作持写锁并原子替换快照，读操作持读锁。
type Engine struct {
	mu     sync.RWMutex
	decl   *declarations
	snap   *snapshot
	logger *slog.Logger
}

// New 创建一个空引擎。logger 为 nil 时丢弃日志。
func New(logger *slog.Logger) *Engine {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	e := &Engine{decl: newDeclarations(), logger: logger}
	e.rebuildLocked()
	return e
}

// rebuildLocked 根据当前全部声明从空白状态全量重算物化视图并原子替换。
// 调用方必须持有写锁（或处于构造阶段）。
//
// 注意：对象类型、链接类型、标签、角色、主体、实例的“声明”本身不改变
// 物化视图的任何内容（视图只由边、挂载、传播、阻断与授权决定），
// 因此这些操作跳过重算；这与每次都重算的结果严格相等。
func (e *Engine) rebuildLocked() {
	e.snap = computeSnapshot(e.decl)
}

// --- 对象类型 / 链接类型 / 标签声明 ---

// AddObjectType 声明一个对象类型。
func (e *Engine) AddObjectType(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.decl.objectTypes[id] {
		return fmt.Errorf("%w: object type %q", ErrDuplicate, id)
	}
	e.decl.objectTypes[id] = true
	return nil
}

// AddLinkType 声明一个链接类型。
func (e *Engine) AddLinkType(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.decl.linkTypes[id] {
		return fmt.Errorf("%w: link type %q", ErrDuplicate, id)
	}
	e.decl.linkTypes[id] = true
	return nil
}

// AddTag 声明一个标签。
func (e *Engine) AddTag(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.decl.tags[id] {
		return fmt.Errorf("%w: tag %q", ErrDuplicate, id)
	}
	e.decl.tags[id] = true
	return nil
}

// AddLinkEdge 在两个对象类型之间添加一条链接边。
func (e *Engine) AddLinkEdge(edge LinkEdge) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.linkTypes[edge.LinkType] {
		return fmt.Errorf("%w: %q", ErrUnknownLinkType, edge.LinkType)
	}
	if !e.decl.objectTypes[edge.From] || !e.decl.objectTypes[edge.To] {
		return fmt.Errorf("%w: %q -> %q", ErrUnknownObjectType, edge.From, edge.To)
	}
	if e.decl.edges[edge] {
		return fmt.Errorf("%w: edge %+v", ErrDuplicate, edge)
	}
	e.decl.edges[edge] = true
	e.rebuildLocked()
	return nil
}

// RemoveLinkEdge 删除一条链接边。
func (e *Engine) RemoveLinkEdge(edge LinkEdge) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.edges[edge] {
		return fmt.Errorf("%w: edge %+v", ErrNotFound, edge)
	}
	delete(e.decl.edges, edge)
	e.rebuildLocked()
	return nil
}

// --- 标签挂载 / 传播 / 阻断 ---

// AttachTag 把标签直接挂载到对象类型上。
func (e *Engine) AttachTag(att Attachment) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.objectTypes[att.ObjectType] {
		return fmt.Errorf("%w: %q", ErrUnknownObjectType, att.ObjectType)
	}
	if !e.decl.tags[att.Tag] {
		return fmt.Errorf("%w: %q", ErrUnknownTag, att.Tag)
	}
	if e.decl.attachments[att] {
		return fmt.Errorf("%w: attachment %+v", ErrDuplicate, att)
	}
	e.decl.attachments[att] = true
	e.rebuildLocked()
	return nil
}

// DetachTag 从对象类型上移除标签。仅因该来源继承到该标签的下游
// 对象类型与实例将级联失去该标签；存在其他独立来源的不受影响。
func (e *Engine) DetachTag(att Attachment) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.attachments[att] {
		return fmt.Errorf("%w: attachment %+v", ErrNotFound, att)
	}
	delete(e.decl.attachments, att)
	e.rebuildLocked()
	return nil
}

// AddPropagation 声明标签沿某链接类型向指定方向传播。
func (e *Engine) AddPropagation(p Propagation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.tags[p.Tag] {
		return fmt.Errorf("%w: %q", ErrUnknownTag, p.Tag)
	}
	if !e.decl.linkTypes[p.LinkType] {
		return fmt.Errorf("%w: %q", ErrUnknownLinkType, p.LinkType)
	}
	if e.decl.propagations[p] {
		return fmt.Errorf("%w: propagation %+v", ErrDuplicate, p)
	}
	e.decl.propagations[p] = true
	e.rebuildLocked()
	return nil
}

// RemovePropagation 移除某链接类型的传播资格。仅经该声明继承到
// 标签的下游对象类型与实例将级联失去该标签。
func (e *Engine) RemovePropagation(p Propagation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.propagations[p] {
		return fmt.Errorf("%w: propagation %+v", ErrNotFound, p)
	}
	delete(e.decl.propagations, p)
	e.rebuildLocked()
	return nil
}

// AddBlock 声明一个传播阻断点。
func (e *Engine) AddBlock(b Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.objectTypes[b.ObjectType] {
		return fmt.Errorf("%w: %q", ErrUnknownObjectType, b.ObjectType)
	}
	if !e.decl.tags[b.Tag] {
		return fmt.Errorf("%w: %q", ErrUnknownTag, b.Tag)
	}
	if e.decl.blocks[b] {
		return fmt.Errorf("%w: block %+v", ErrDuplicate, b)
	}
	e.decl.blocks[b] = true
	e.rebuildLocked()
	return nil
}

// RemoveBlock 移除一个传播阻断点。
func (e *Engine) RemoveBlock(b Block) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.blocks[b] {
		return fmt.Errorf("%w: block %+v", ErrNotFound, b)
	}
	delete(e.decl.blocks, b)
	e.rebuildLocked()
	return nil
}

// --- 角色 / 主体 / 实例 ---

// AddRole 声明一个角色及其上级角色。允许上级关系构成环；
// 环在判定时被检测并以 ReasonRoleCycle 汇报。
func (e *Engine) AddRole(id string, parents ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.decl.roleParents[id]; ok {
		return fmt.Errorf("%w: role %q", ErrDuplicate, id)
	}
	for _, p := range parents {
		if _, ok := e.decl.roleParents[p]; !ok && p != id {
			return fmt.Errorf("%w: parent role %q", ErrUnknownRole, p)
		}
	}
	e.decl.roleParents[id] = map[string]bool{}
	for _, p := range parents {
		e.decl.roleParents[id][p] = true
	}
	return nil
}

// AddGrant 为角色添加对标签的允许/拒绝声明。
func (e *Engine) AddGrant(g Grant) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.decl.roleParents[g.Role]; !ok {
		return fmt.Errorf("%w: %q", ErrUnknownRole, g.Role)
	}
	if !e.decl.tags[g.Tag] {
		return fmt.Errorf("%w: %q", ErrUnknownTag, g.Tag)
	}
	if e.decl.grants[g] {
		return fmt.Errorf("%w: grant %+v", ErrDuplicate, g)
	}
	e.decl.grants[g] = true
	e.rebuildLocked()
	return nil
}

// RemoveGrant 移除一条授权声明。
func (e *Engine) RemoveGrant(g Grant) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.grants[g] {
		return fmt.Errorf("%w: grant %+v", ErrNotFound, g)
	}
	delete(e.decl.grants, g)
	e.rebuildLocked()
	return nil
}

// AddSubject 声明一个主体及其所属角色。
func (e *Engine) AddSubject(id string, roles ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.decl.subjectRoles[id]; ok {
		return fmt.Errorf("%w: subject %q", ErrDuplicate, id)
	}
	for _, r := range roles {
		if _, ok := e.decl.roleParents[r]; !ok {
			return fmt.Errorf("%w: role %q", ErrUnknownRole, r)
		}
	}
	e.decl.subjectRoles[id] = map[string]bool{}
	for _, r := range roles {
		e.decl.subjectRoles[id][r] = true
	}
	return nil
}

// AddInstance 声明一个实例及其所属对象类型。
func (e *Engine) AddInstance(id, objectType string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.decl.objectTypes[objectType] {
		return fmt.Errorf("%w: %q", ErrUnknownObjectType, objectType)
	}
	if _, ok := e.decl.instances[id]; ok {
		return fmt.Errorf("%w: instance %q", ErrDuplicate, id)
	}
	e.decl.instances[id] = objectType
	return nil
}

// ErrNotFound 表示试图删除不存在的声明。
var ErrNotFound = errors.New("ontology: declaration not found")
