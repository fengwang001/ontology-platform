package ontology

import (
	"errors"
	"sort"
	"sync"
)

// 常见错误。
var (
	ErrInvalidCaller = errors.New("ontology: invalid caller id")
	ErrNotFound      = errors.New("ontology: object or link not found")
	ErrDuplicate     = errors.New("ontology: duplicate id or link")
	ErrSelfLoop      = errors.New("ontology: link type does not allow self loops")
	ErrDanglingLink  = errors.New("ontology: link endpoint object does not exist")
	ErrInvalidType   = errors.New("ontology: unknown or invalid type")
)

// Graph 是本体链接图，包含对象、链接以及按调用者维护的权限状态。
// 所有方法均支持并发调用；HasCycle 与增删链接、权限变更之间满足
// 可线性化的串行等价语义：每次 HasCycle 在 RWMutex 的读锁下取得一个
// 完整一致的状态快照，等价于与所有写操作互斥地排在某个串行时刻。
type Graph struct {
	mu sync.RWMutex

	objectTypes map[string]*ObjectType
	linkTypes   map[string]*LinkType

	objects map[string]*objectState
	links   map[string]*Link

	// 每个对象关联的链接 ID（源或目标任一匹配）。
	objLinks map[string]map[string]struct{}

	// 调用者集合（出现过即登记），空字符串为非法调用者。
	callers map[string]struct{}

	// 存在性权限：caller -> 对象 ID 集合。集合中没有即不可见。
	existPerm map[string]map[string]struct{}
	// 遍历权限：caller -> 链接 ID 集合。集合中没有即不可遍历。
	traversePerm map[string]map[string]struct{}
}

type objectState struct {
	id  string
	typ *ObjectType
}

// NewGraph 创建一个空图。
func NewGraph() *Graph {
	return &Graph{
		objectTypes:  map[string]*ObjectType{},
		linkTypes:    map[string]*LinkType{},
		objects:      map[string]*objectState{},
		links:        map[string]*Link{},
		objLinks:     map[string]map[string]struct{}{},
		callers:      map[string]struct{}{},
		existPerm:    map[string]map[string]struct{}{},
		traversePerm: map[string]map[string]struct{}{},
	}
}

// validCallerLocked 判定调用者标识是否合法。规则：非空且此前已登记。
func (g *Graph) validCallerLocked(caller string) bool {
	if caller == "" {
		return false
	}
	_, ok := g.callers[caller]
	return ok
}

// RegisterCaller 登记一个合法调用者标识。空字符串非法。
func (g *Graph) RegisterCaller(caller string) error {
	if caller == "" {
		return ErrInvalidCaller
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callers[caller] = struct{}{}
	if g.existPerm[caller] == nil {
		g.existPerm[caller] = map[string]struct{}{}
	}
	if g.traversePerm[caller] == nil {
		g.traversePerm[caller] = map[string]struct{}{}
	}
	return nil
}

// AddObjectType 注册对象类型。
func (g *Graph) AddObjectType(t *ObjectType) error {
	if t == nil || t.ID == "" {
		return ErrInvalidType
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objectTypes[t.ID]; ok {
		return ErrDuplicate
	}
	g.objectTypes[t.ID] = t
	return nil
}

// AddLinkType 注册链接类型。
func (g *Graph) AddLinkType(t *LinkType) error {
	if t == nil || t.ID == "" {
		return ErrInvalidType
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.linkTypes[t.ID]; ok {
		return ErrDuplicate
	}
	g.linkTypes[t.ID] = t
	return nil
}

// AddObject 创建对象。
func (g *Graph) AddObject(id, typeID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.objectTypes[typeID]
	if !ok {
		return ErrInvalidType
	}
	if _, ok := g.objects[id]; ok {
		return ErrDuplicate
	}
	g.objects[id] = &objectState{id: id, typ: t}
	g.objLinks[id] = map[string]struct{}{}
	return nil
}

// DeleteObject 删除对象及其关联的全部链接（对所有调用者均消失）。
func (g *Graph) DeleteObject(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.objects[id]; !ok {
		return ErrNotFound
	}
	for lid := range g.objLinks[id] {
		l := g.links[lid]
		if l == nil {
			continue
		}
		other := l.Source
		if other == id {
			other = l.Target
		}
		delete(g.objLinks[other], lid)
		for _, set := range g.traversePerm {
			delete(set, lid)
		}
		delete(g.links, lid)
	}
	delete(g.objLinks, id)
	for _, set := range g.existPerm {
		delete(set, id)
	}
	delete(g.objects, id)
	return nil
}

// AddLink 创建链接并校验链接类型约束（自环、同对象对多重链接）。
// 创建时不自动授予任何调用者遍历权限。
func (g *Graph) AddLink(l *Link) error {
	if l == nil || l.ID == "" || l.Type == nil {
		return ErrInvalidType
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	lt, ok := g.linkTypes[l.Type.ID]
	if !ok {
		return ErrInvalidType
	}
	if _, ok := g.links[l.ID]; ok {
		return ErrDuplicate
	}
	if _, ok := g.objects[l.Source]; !ok {
		return ErrDanglingLink
	}
	if _, ok := g.objects[l.Target]; !ok {
		return ErrDanglingLink
	}
	if l.Source == l.Target && !lt.AllowSelfLoop {
		return ErrSelfLoop
	}
	if !lt.AllowMultiple {
		for lid := range g.objLinks[l.Source] {
			existing := g.links[lid]
			if existing != nil && existing.Type == lt &&
				((existing.Source == l.Source && existing.Target == l.Target) ||
					(existing.Source == l.Target && existing.Target == l.Source)) {
				return ErrDuplicate
			}
		}
	}
	l.Type = lt // 以注册的类型定义为准，避免调用方持有同 ID 的另一份副本
	g.links[l.ID] = l
	g.objLinks[l.Source][l.ID] = struct{}{}
	if l.Target != l.Source {
		g.objLinks[l.Target][l.ID] = struct{}{}
	}
	return nil
}

// DeleteLink 删除一条链接。
func (g *Graph) DeleteLink(id string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	l, ok := g.links[id]
	if !ok {
		return ErrNotFound
	}
	delete(g.objLinks[l.Source], id)
	delete(g.objLinks[l.Target], id)
	for _, set := range g.traversePerm {
		delete(set, id)
	}
	delete(g.links, id)
	return nil
}

// GrantExist / RevokeExist 授予与撤销调用者对对象的存在性权限。
func (g *Graph) GrantExist(caller, objectID string) error {
	return g.setExist(caller, objectID, true)
}

func (g *Graph) RevokeExist(caller, objectID string) error {
	return g.setExist(caller, objectID, false)
}

func (g *Graph) setExist(caller, objectID string, grant bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.validCallerLocked(caller) {
		return ErrInvalidCaller
	}
	if _, ok := g.objects[objectID]; !ok {
		return ErrNotFound
	}
	if grant {
		g.existPerm[caller][objectID] = struct{}{}
	} else {
		delete(g.existPerm[caller], objectID)
	}
	return nil
}

// GrantTraverse / RevokeTraverse 授予与撤销调用者对链接的遍历权限。
func (g *Graph) GrantTraverse(caller, linkID string) error {
	return g.setTraverse(caller, linkID, true)
}

func (g *Graph) RevokeTraverse(caller, linkID string) error {
	return g.setTraverse(caller, linkID, false)
}

func (g *Graph) setTraverse(caller, linkID string, grant bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.validCallerLocked(caller) {
		return ErrInvalidCaller
	}
	if _, ok := g.links[linkID]; !ok {
		return ErrNotFound
	}
	if grant {
		g.traversePerm[caller][linkID] = struct{}{}
	} else {
		delete(g.traversePerm[caller], linkID)
	}
	return nil
}

// snapshot 是调用者在某一确定时刻、按题面次序过滤后得到的可见子图。
// 快照在锁内构建完成后释放锁；后续判定不再访问 Graph，因此不可能看到
// 任何写操作的中间状态。
type snapshot struct {
	// 第一步剔除后剩余的可见对象（已按 ID 排序）。
	objects []string
	visible map[string]struct{}
	// 第二步剔除后、判定实际使用的有向弧。
	// 每个链接对调用者展开为至多两条（双向）或一条（单向）弧；
	// 同一对象对之间的多重同类型链接去重，不重复计入证据与度量。
	out   map[string][]string
	count int
	// 第一步后保留、第二步被遍历权限排除的链接数，以及最终参与判定的
	// 去重链接数。度量只统计这些可见范围内的数字。
	candidateLinks int
	usedLinks      int
}

// takeSnapshot 必须在持有 g.mu（读锁或写锁）时调用。
func (g *Graph) takeSnapshot(caller string) (*snapshot, bool) {
	if !g.validCallerLocked(caller) {
		return nil, false
	}
	s := &snapshot{
		visible: map[string]struct{}{},
		out:     map[string][]string{},
	}
	existSet := g.existPerm[caller]
	travSet := g.traversePerm[caller]

	// 第一步：存在性权限过滤。只枚举被授权的对象，不扫描全部对象——
	// 对调用者完全不可见的对象不在此步骤产生任何访问开销。
	for id := range existSet {
		if _, ok := g.objects[id]; ok {
			s.visible[id] = struct{}{}
			s.objects = append(s.objects, id)
		}
	}
	sort.Strings(s.objects)
	s.count = len(s.objects)

	// 去重后的有向邻接：map[源]map[目标]（多重链接不重复）。
	adj := map[string]map[string]struct{}{}

	// 仅枚举可见对象关联的链接；不可见对象之间的链接完全不被触碰。
	seenLinks := map[string]struct{}{}
	for id := range s.visible {
		for lid := range g.objLinks[id] {
			if _, dup := seenLinks[lid]; dup {
				continue
			}
			seenLinks[lid] = struct{}{}
			l := g.links[lid]
			if l == nil {
				continue
			}
			// 关联链接的另一端不可见时，随对象剔除一并剔除。
			if _, ok := s.visible[l.Source]; !ok {
				continue
			}
			if _, ok := s.visible[l.Target]; !ok {
				continue
			}
			s.candidateLinks++
			// 第二步：遍历权限过滤。
			if _, canTraverse := travSet[lid]; !canTraverse {
				continue
			}
			s.usedLinks++
			addArc := func(from, to string) {
				if adj[from] == nil {
					adj[from] = map[string]struct{}{}
				}
				adj[from][to] = struct{}{}
			}
			addArc(l.Source, l.Target)
			if l.Type.Direction == Bidirectional {
				addArc(l.Target, l.Source)
			}
		}
	}

	for from, tos := range adj {
		list := make([]string, 0, len(tos))
		for to := range tos {
			list = append(list, to)
		}
		sort.Strings(list)
		s.out[from] = list
	}
	return s, true
}

// ArcView 是某次调用时刻、经规定权限次序过滤后的可见弧只读视图。
// 用于内部校验与诊断；它同样是在锁内一次性构建的一致快照。
type ArcView struct {
	Objects []string
	arcs    map[string]map[string]struct{}
}

// HasArc 报告从 from 到 to 的可遍历弧是否存在。
func (v *ArcView) HasArc(from, to string) bool {
	_, ok := v.arcs[from][to]
	return ok
}

// SupportCycle 校验给定环序列的每条闭合弧都存在于该视图中，
// 且序列是无重复顶点的简单环。
func (v *ArcView) SupportCycle(cycle []string) bool {
	if len(cycle) == 0 {
		return false
	}
	seen := map[string]struct{}{}
	for _, id := range cycle {
		if _, dup := seen[id]; dup {
			return false
		}
		seen[id] = struct{}{}
	}
	for i := range cycle {
		if !v.HasArc(cycle[i], cycle[(i+1)%len(cycle)]) {
			return false
		}
	}
	return true
}

// VisibleArcs 返回调用者当前可见且可遍历子图的一致只读弧视图。
func (g *Graph) VisibleArcs(caller string) (*ArcView, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	s, ok := g.takeSnapshot(caller)
	if !ok {
		return nil, ErrInvalidCaller
	}
	v := &ArcView{
		Objects: s.objects,
		arcs:    map[string]map[string]struct{}{},
	}
	for from, tos := range s.out {
		v.arcs[from] = map[string]struct{}{}
		for _, to := range tos {
			v.arcs[from][to] = struct{}{}
		}
	}
	return v, nil
}
