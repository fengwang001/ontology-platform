package ontology

// 本文件是独立于产品实现的「朴素日志重放模型」，仅用于测试对照：
// 状态用普通 map/切片平铺，重放逐条 apply，最短路径用暴力 DFS 枚举
// 全部简单路径后取 (代价, 字典序) 最小者。其实现不引用 service.go /
// shortestpath.go 的任何逻辑，以保证对照的独立性。

import "sort"

type naiveModel struct {
	objTypes  map[ObjectTypeID]bool
	linkTypes map[LinkTypeID]LinkType
	objType   map[ObjectID]ObjectTypeID
	active    map[ObjectID]bool
	links     map[[3]string]bool // (linkType, from, to)
	perms     map[Permission]bool
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		objTypes:  make(map[ObjectTypeID]bool),
		linkTypes: make(map[LinkTypeID]LinkType),
		objType:   make(map[ObjectID]ObjectTypeID),
		active:    make(map[ObjectID]bool),
		links:     make(map[[3]string]bool),
		perms:     make(map[Permission]bool),
	}
}

func (n *naiveModel) can(user UserID, a Action, r Resource) bool {
	if user == AdminUser {
		return true
	}
	return n.perms[Permission{User: user, Action: a, Resource: r}]
}

// apply 假定操作已被接受（重放语义），不做任何校验。
func (n *naiveModel) apply(op Operation) {
	switch op.Kind {
	case OpDeclareObjectType:
		n.objTypes[op.ObjectType] = true
	case OpDeclareLinkType:
		n.linkTypes[op.LinkSpec.ID] = *op.LinkSpec
	case OpCreateObject:
		n.objType[op.Object] = op.ObjectType
		n.active[op.Object] = true
	case OpInvalidateObject:
		n.active[op.Object] = false
		for key := range n.links {
			if key[1] == string(op.Object) || key[2] == string(op.Object) {
				delete(n.links, key)
			}
		}
	case OpCreateLink:
		n.links[[3]string{string(op.LinkType), string(op.From), string(op.To)}] = true
	case OpRemoveLink:
		delete(n.links, [3]string{string(op.LinkType), string(op.From), string(op.To)})
	case OpGrantPermission:
		n.perms[*op.Permission] = true
	case OpRevokePermission:
		delete(n.perms, *op.Permission)
	}
}

// validate 独立重写的四级拒绝判定，次序：参数非法 > 权限不足 > 基数上限。
func (n *naiveModel) validate(op Operation, caller UserID) *RejectError {
	if r := n.checkParams(op); r != nil {
		return r
	}
	if r := n.checkPerm(op, caller); r != nil {
		return r
	}
	return n.checkCardinality(op)
}

func (n *naiveModel) checkParams(op Operation) *RejectError {
	bad := func(d string) *RejectError { return &RejectError{Kind: RejectInvalidParams, Detail: d} }
	switch op.Kind {
	case OpDeclareObjectType:
		if op.ObjectType == "" || n.objTypes[op.ObjectType] {
			return bad("bad object type decl")
		}
	case OpDeclareLinkType:
		sp := op.LinkSpec
		if sp == nil || sp.ID == "" || sp.Cost <= 0 || sp.MaxOutgoing < 0 || sp.MaxIncoming < 0 {
			return bad("bad link spec")
		}
		if n.linkTypes[sp.ID].ID != "" {
			return bad("dup link type")
		}
		if !n.objTypes[sp.FromType] || !n.objTypes[sp.ToType] {
			return bad("unknown endpoint type")
		}
	case OpCreateObject:
		if op.Object == "" || !n.objTypes[op.ObjectType] {
			return bad("bad object")
		}
		if _, ok := n.objType[op.Object]; ok {
			return bad("dup object")
		}
	case OpInvalidateObject:
		if _, ok := n.objType[op.Object]; !ok || !n.active[op.Object] {
			return bad("object not active")
		}
	case OpCreateLink:
		lt, ok := n.linkTypes[op.LinkType]
		if !ok {
			return bad("unknown link type")
		}
		if !n.active[op.From] || !n.active[op.To] {
			return bad("endpoint inactive")
		}
		if n.objType[op.From] != lt.FromType || n.objType[op.To] != lt.ToType {
			return bad("endpoint type mismatch")
		}
		if n.links[[3]string{string(op.LinkType), string(op.From), string(op.To)}] {
			return bad("dup link")
		}
	case OpRemoveLink:
		if !n.links[[3]string{string(op.LinkType), string(op.From), string(op.To)}] {
			return bad("link missing")
		}
	case OpGrantPermission, OpRevokePermission:
		p := op.Permission
		if p == nil || p.User == "" {
			return bad("bad permission")
		}
		if p.Action != ActionRead && p.Action != ActionWrite {
			return bad("bad action")
		}
		switch p.Resource.Kind {
		case ResourceObjectType:
			if !n.objTypes[ObjectTypeID(p.Resource.ID)] {
				return bad("unknown resource")
			}
		case ResourceLinkType:
			if _, ok := n.linkTypes[LinkTypeID(p.Resource.ID)]; !ok {
				return bad("unknown resource")
			}
		default:
			return bad("bad resource kind")
		}
		if op.Kind == OpGrantPermission && n.perms[*p] {
			return bad("dup grant")
		}
		if op.Kind == OpRevokePermission && !n.perms[*p] {
			return bad("grant missing")
		}
	default:
		return bad("unknown op")
	}
	return nil
}

func (n *naiveModel) checkPerm(op Operation, caller UserID) *RejectError {
	deny := &RejectError{Kind: RejectPermission, Detail: "denied"}
	switch op.Kind {
	case OpDeclareObjectType, OpDeclareLinkType, OpGrantPermission, OpRevokePermission:
		if caller != AdminUser {
			return deny
		}
	case OpCreateObject:
		if !n.can(caller, ActionWrite, Resource{Kind: ResourceObjectType, ID: string(op.ObjectType)}) {
			return deny
		}
	case OpInvalidateObject:
		if !n.can(caller, ActionWrite, Resource{Kind: ResourceObjectType, ID: string(n.objType[op.Object])}) {
			return deny
		}
	case OpCreateLink, OpRemoveLink:
		if !n.can(caller, ActionWrite, Resource{Kind: ResourceLinkType, ID: string(op.LinkType)}) {
			return deny
		}
	}
	return nil
}

func (n *naiveModel) checkCardinality(op Operation) *RejectError {
	if op.Kind != OpCreateLink {
		return nil
	}
	lt := n.linkTypes[op.LinkType]
	out, in := 0, 0
	for key := range n.links {
		if key[0] != string(op.LinkType) {
			continue
		}
		if key[1] == string(op.From) {
			out++
		}
		if key[2] == string(op.To) {
			in++
		}
		if !lt.Directed {
			if key[2] == string(op.From) {
				out++
			}
			if key[1] == string(op.To) {
				in++
			}
		}
	}
	if lt.MaxOutgoing > 0 && out >= lt.MaxOutgoing {
		return &RejectError{Kind: RejectCardinality, Detail: "max outgoing"}
	}
	if lt.MaxIncoming > 0 && in >= lt.MaxIncoming {
		return &RejectError{Kind: RejectCardinality, Detail: "max incoming"}
	}
	return nil
}

// shortestPath 暴力枚举全部简单路径，取 (代价, 对象序列字典序) 最小者。
func (n *naiveModel) shortestPath(start, end ObjectID, caller UserID) Path {
	notFound := Path{Found: false}
	visibleObj := func(id ObjectID) bool {
		t, ok := n.objType[id]
		return ok && n.active[id] &&
			n.can(caller, ActionRead, Resource{Kind: ResourceObjectType, ID: string(t)})
	}
	if !visibleObj(start) || !visibleObj(end) {
		return notFound
	}
	if start == end {
		return Path{Objects: []ObjectID{start}, Cost: 0, Found: true}
	}
	// 汇总可见邻接表。
	adj := map[ObjectID][]struct {
		to   ObjectID
		cost int64
	}{}
	for key := range n.links {
		lt := n.linkTypes[LinkTypeID(key[0])]
		from, to := ObjectID(key[1]), ObjectID(key[2])
		if !n.can(caller, ActionRead, Resource{Kind: ResourceLinkType, ID: key[0]}) {
			continue
		}
		if !visibleObj(from) || !visibleObj(to) {
			continue
		}
		adj[from] = append(adj[from], struct {
			to   ObjectID
			cost int64
		}{to, lt.Cost})
		if !lt.Directed {
			adj[to] = append(adj[to], struct {
				to   ObjectID
				cost int64
			}{from, lt.Cost})
		}
	}
	var bestPath []ObjectID
	var bestCost int64
	found := false
	visited := map[ObjectID]bool{start: true}
	var dfs func(cur ObjectID, cost int64, path []ObjectID)
	dfs = func(cur ObjectID, cost int64, path []ObjectID) {
		if cur == end {
			if !found || pathLess(cost, path, bestCost, bestPath) {
				found, bestCost = true, cost
				bestPath = append([]ObjectID(nil), path...)
			}
			return
		}
		nexts := append([]struct {
			to   ObjectID
			cost int64
		}(nil), adj[cur]...)
		sort.Slice(nexts, func(i, j int) bool { return nexts[i].to < nexts[j].to })
		for _, e := range nexts {
			if visited[e.to] {
				continue
			}
			visited[e.to] = true
			dfs(e.to, cost+e.cost, append(path, e.to))
			delete(visited, e.to)
		}
	}
	dfs(start, 0, []ObjectID{start})
	if !found {
		return notFound
	}
	return Path{Objects: bestPath, Cost: bestCost, Found: true}
}
