package ontology

import (
	"fmt"
	"sort"
)

// Naive 是独立实现的朴素对照模型：不重放任何索引结构，
// 而是把全部操作按版本顺序重放到平面 map 上重建 AsOf 时刻状态，
// 再用自己的 BFS 展开。用于与 Store.Traverse 的结果逐条对照。
type Naive struct {
	ops []naiveOp
}

type naiveOp struct {
	v       Version
	kind    string
	typeID  string
	objID   string
	propID  string
	propNm  string
	val     Value
	props   []PropertyDef
	add     []PropertyDef
	drop    []string
	link    LinkID
	fromTy  string
	toTy    string
	card    Cardinality
	horizon Version
	gk      gapKind
	scope   string
	to      Version
}

func (n *Naive) rec(op naiveOp) { n.ops = append(n.ops, op) }

func (n *Naive) DefineObjectType(v Version, typeID string, props []PropertyDef) {
	n.rec(naiveOp{v: v, kind: "define-object-type", typeID: typeID, props: cloneProps(props)})
}

func (n *Naive) MigrateObjectType(v Version, typeID string, add []PropertyDef, drop []string) {
	n.rec(naiveOp{v: v, kind: "migrate-object-type", typeID: typeID, add: cloneProps(add), drop: append([]string(nil), drop...)})
}

func (n *Naive) DefineLinkType(v Version, typeID, fromType, toType string, card Cardinality) {
	n.rec(naiveOp{v: v, kind: "define-link-type", typeID: typeID, fromTy: fromType, toTy: toType, card: card})
}

func (n *Naive) AdjustCardinality(v Version, typeID string, card Cardinality) {
	n.rec(naiveOp{v: v, kind: "adjust-cardinality", typeID: typeID, card: card})
}

func (n *Naive) PutObject(v Version, typeID, objID string, props map[string]Value) {
	n.rec(naiveOp{v: v, kind: "put-object", typeID: typeID, objID: objID})
	for name, val := range props {
		n.rec(naiveOp{v: v, kind: "put-prop", typeID: typeID, objID: objID, propNm: name, val: val})
	}
}

func (n *Naive) SetProperty(v Version, objID, propName string, val Value) {
	n.rec(naiveOp{v: v, kind: "set-property", objID: objID, propNm: propName, val: val})
}

func (n *Naive) DeleteObject(v Version, objID string) {
	n.rec(naiveOp{v: v, kind: "delete-object", objID: objID})
}

func (n *Naive) AddLink(v Version, typeID, from, to string) {
	n.rec(naiveOp{v: v, kind: "add-link", link: LinkID{Type: typeID, From: from, To: to}})
}

func (n *Naive) RemoveLink(v Version, typeID, from, to string) {
	n.rec(naiveOp{v: v, kind: "remove-link", link: LinkID{Type: typeID, From: from, To: to}})
}

func (n *Naive) Compact(keepFrom Version) {
	n.rec(naiveOp{v: 0, kind: "compact", horizon: keepFrom})
}

func (n *Naive) DeclareGap(kind, scope string, from, to Version) {
	var gk gapKind
	switch kind {
	case "object-props":
		gk = gapObjectProps
	case "object-life":
		gk = gapObjectLife
	case "link":
		gk = gapLink
	case "link-type":
		gk = gapLinkType
	case "schema":
		gk = gapSchema
	default:
		return
	}
	n.rec(naiveOp{v: 0, kind: "gap", gk: gk, scope: scope, horizon: from, to: to})
}

// naiveState 是重放 ≤ asOf 的全部操作后得到的平面状态。
type naiveState struct {
	horizon  Version
	objType  map[string]string
	alive    map[string]bool
	props    map[string]map[string]Value  // objID -> propID -> value
	defs     map[string][]PropertyDef     // typeID -> 当前定义
	propIDs  map[string]map[string]string // typeID -> propName -> propID
	links    map[LinkID]bool
	known    map[LinkID]bool
	out      map[string][]LinkID
	linkFrom map[string]string
	gaps     []naiveOp
}

func (n *Naive) replay(asOf Version) *naiveState {
	st := &naiveState{
		objType:  map[string]string{},
		alive:    map[string]bool{},
		props:    map[string]map[string]Value{},
		defs:     map[string][]PropertyDef{},
		propIDs:  map[string]map[string]string{},
		links:    map[LinkID]bool{},
		known:    map[LinkID]bool{},
		out:      map[string][]LinkID{},
		linkFrom: map[string]string{},
	}
	propIDOf := func(typeID, name string) string {
		m := st.propIDs[typeID]
		if m == nil {
			return ""
		}
		return m[name]
	}
	for _, op := range n.ops {
		if op.v != 0 && op.v > asOf {
			continue
		}
		switch op.kind {
		case "define-object-type":
			st.defs[op.typeID] = cloneProps(op.props)
			m := map[string]string{}
			for _, p := range op.props {
				m[p.Name] = p.ID
			}
			st.propIDs[op.typeID] = m
		case "migrate-object-type":
			dropped := map[string]bool{}
			for _, d := range op.drop {
				dropped[d] = true
			}
			replaced := map[string]bool{}
			for _, p := range op.add {
				replaced[p.ID] = true
			}
			var next []PropertyDef
			for _, p := range st.defs[op.typeID] {
				if dropped[p.ID] || replaced[p.ID] {
					continue
				}
				next = append(next, p)
			}
			next = append(next, op.add...)
			st.defs[op.typeID] = next
			m := map[string]string{}
			for _, p := range next {
				m[p.Name] = p.ID
			}
			st.propIDs[op.typeID] = m
		case "put-object":
			st.objType[op.objID] = op.typeID
			st.alive[op.objID] = true
			if st.props[op.objID] == nil {
				st.props[op.objID] = map[string]Value{}
			}
		case "put-prop", "set-property":
			if st.props[op.objID] == nil {
				st.props[op.objID] = map[string]Value{}
			}
			st.props[op.objID][propIDOf(op.typeIDOf(st), op.propNm)] = op.val
		case "delete-object":
			st.alive[op.objID] = false
		case "add-link":
			st.links[op.link] = true
			if !st.known[op.link] {
				st.known[op.link] = true
				st.out[op.link.From] = insertSortedLink(st.out[op.link.From], op.link)
			}
		case "remove-link":
			delete(st.links, op.link)
		case "compact":
			if op.horizon > st.horizon {
				st.horizon = op.horizon
			}
		case "gap":
			st.gaps = append(st.gaps, op)
		}
	}
	return st
}

// typeIDOf 是重放期解析属性名到 propID 的辅助。
func (op naiveOp) typeIDOf(st *naiveState) string { return st.objType[op.objID] }

func (st *naiveState) gapHit(k gapKind, scope string, v Version) bool {
	for _, g := range st.gaps {
		if g.gk == k && g.scope == scope && g.horizon <= v && v < g.to {
			return true
		}
	}
	return false
}

// Traverse 是朴素模型的独立展开实现。
func (n *Naive) Traverse(req TraverseRequest) (*TraverseResult, error) {
	st := n.replay(req.AsOf)

	// 优先级 1：起始对象不存在。
	if req.AsOf >= st.horizon {
		if st.gapHit(gapObjectLife, req.Start, req.AsOf) {
			// 无法判定，落到缺失错误（在边界检查之后返回）。
		} else if !st.alive[req.Start] {
			return nil, &TraverseError{Kind: ErrKindStartNotExist,
				Detail: fmt.Sprintf("start object %q does not exist at v%d", req.Start, req.AsOf)}
		}
	}
	// 优先级 2：早于边界。
	if req.AsOf < st.horizon {
		return nil, &TraverseError{Kind: ErrKindBeforeHorizon,
			Detail: fmt.Sprintf("asOf v%d is before replay horizon v%d", req.AsOf, st.horizon)}
	}
	if st.gapHit(gapObjectLife, req.Start, req.AsOf) {
		return nil, &TraverseError{Kind: ErrKindHistoryMissing,
			Detail: fmt.Sprintf("object-life history of %q missing at v%d", req.Start, req.AsOf)}
	}

	res := &TraverseResult{AsOf: req.AsOf}
	visited := map[string]bool{req.Start: true}
	if len(visited) > req.MaxVisited {
		return nil, &TraverseError{Kind: ErrKindLimitExceeded,
			Detail: fmt.Sprintf("visited count exceeds declared limit %d", req.MaxVisited)}
	}
	type item struct {
		id    string
		depth int
	}
	queue := []item{{req.Start, 0}}

	resolve := func(id string, depth int) (NodeSnapshot, *TraverseError) {
		if st.gapHit(gapObjectProps, id, req.AsOf) || st.gapHit(gapSchema, st.objType[id], req.AsOf) {
			return NodeSnapshot{}, &TraverseError{Kind: ErrKindHistoryMissing,
				Detail: fmt.Sprintf("property/schema history of %q missing", id)}
		}
		props := map[string]Value{}
		for _, pd := range st.defs[st.objType[id]] {
			if val, ok := st.props[id][pd.ID]; ok {
				props[pd.Name] = val
			}
		}
		return NodeSnapshot{ID: id, Type: st.objType[id], Depth: depth, Props: props}, nil
	}

	snap, terr := resolve(req.Start, 0)
	if terr != nil {
		return nil, terr
	}
	res.Nodes = append(res.Nodes, snap)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		var present []LinkID
		var missingErr *TraverseError
		for _, id := range st.out[cur.id] {
			if st.gapHit(gapLinkType, id.Type, req.AsOf) ||
				st.gapHit(gapLink, id.Type+"/"+id.From+"/"+id.To, req.AsOf) {
				if missingErr == nil {
					missingErr = &TraverseError{Kind: ErrKindHistoryMissing,
						Detail: fmt.Sprintf("cannot determine link %v at v%d", id, req.AsOf)}
				}
				continue
			}
			if st.links[id] {
				present = append(present, id)
			}
		}
		for _, id := range present {
			res.Edges = append(res.Edges, EdgeSnapshot{Type: id.Type, From: id.From, To: id.To})
			if st.gapHit(gapObjectLife, id.To, req.AsOf) {
				if missingErr == nil {
					missingErr = &TraverseError{Kind: ErrKindHistoryMissing,
						Detail: fmt.Sprintf("object-life history of %q missing at v%d", id.To, req.AsOf)}
				}
				continue
			}
			if !st.alive[id.To] || visited[id.To] {
				continue
			}
			if cur.depth+1 > req.MaxDepth {
				return nil, &TraverseError{Kind: ErrKindLimitExceeded,
					Detail: fmt.Sprintf("traversal depth exceeds declared limit %d", req.MaxDepth)}
			}
			visited[id.To] = true
			if len(visited) > req.MaxVisited {
				return nil, &TraverseError{Kind: ErrKindLimitExceeded,
					Detail: fmt.Sprintf("visited count exceeds declared limit %d", req.MaxVisited)}
			}
			snap, terr := resolve(id.To, cur.depth+1)
			if terr != nil {
				if missingErr == nil {
					missingErr = terr
				}
				continue
			}
			res.Nodes = append(res.Nodes, snap)
			queue = append(queue, item{id.To, cur.depth + 1})
		}
		if missingErr != nil {
			return nil, missingErr
		}
	}
	return res, nil
}

// normalize 将遍历结果整理为可逐条对照的规范形式。
func normalize(res *TraverseResult) (nodes []string, edges []string) {
	for _, n := range res.Nodes {
		keys := make([]string, 0, len(n.Props))
		for k := range n.Props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		s := fmt.Sprintf("%s|%s|d%d", n.ID, n.Type, n.Depth)
		for _, k := range keys {
			s += fmt.Sprintf("|%s=%v", k, n.Props[k])
		}
		nodes = append(nodes, s)
	}
	for _, e := range res.Edges {
		edges = append(edges, fmt.Sprintf("%s|%s->%s", e.Type, e.From, e.To))
	}
	return nodes, edges
}
