package hypcheck

import "sort"

// permState 是 t 时刻权限继承关系的重建器：节点存在性、继承边、
// 直接授权全部来自不可变快照存储的 as-of 视图，本身不缓存任何时刻状态。
type permState struct {
	nodes  *snapshotMap[bool]
	edges  *snapshotMap[bool] // key: child + "\x00" + parent（只按子查父）
	grants *snapshotMap[bool] // key: node + "\x00" + typeID
}

func newPermState() *permState {
	return &permState{
		nodes:  newSnapshotMap[bool](),
		edges:  newSnapshotMap[bool](),
		grants: newSnapshotMap[bool](),
	}
}

// nodeExists 查询调用者在 t 时刻是否存在（用于 E3 判定）。
// 存储尚无数据即调用者当时不存在（E3）；真正的缺口由 Precheck 入口判定 E2。
type permView struct {
	nodes  *mapView[bool]
	edges  *mapView[bool]
	grants *mapView[bool]
}

func (p *permState) view(stats *ProbeStats) *permView {
	return &permView{nodes: p.nodes.with(stats), edges: p.edges.with(stats), grants: p.grants.with(stats)}
}

func (p *permView) nodeExists(node string, t Timestamp) (exists, covered bool) {
	v, _, covered, found := p.nodes.asOf(node, t)
	if !covered {
		return false, true
	}
	return found && v, true
}

// check 沿继承图向上重建调用者在 t 时刻的权限快照：
// 自调用者起按“入边（parent 是 child 的上层）”逐层 BFS，
// 对到达的每个节点查询直接授权；找到任一有效授权即允许。
// covered=false 表示重建触及历史缺口。
func (p *permView) check(caller, typeID string, t Timestamp) (trace *PermTrace, covered bool) {
	tr := &PermTrace{Caller: caller}
	visited := map[string]bool{caller: true}
	queue := []string{caller}
	edgeRoot, ok := p.edges.rootAt(t)
	if !ok {
		edgeRoot = nil
	}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		tr.Ancestors = append(tr.Ancestors, node)

		gk := grantKey(node, typeID)
		gv, gat, gc, gfound := p.grants.asOf(gk, t)
		if !gc {
			// 授权存储尚无数据：当时无任何授权，按“未授权”继续。
			gv, gat, gc, gfound = false, 0, true, false
		}
		granted := gfound && gv
		tr.Grants = append(tr.Grants, GrantSnapshot{Node: node, TypeID: typeID, Granted: granted, Effective: gat})
		if granted && !tr.Allowed {
			tr.Allowed = true
			tr.GrantedBy = node
		}

		var parents []pair
		prefix := node + "\x00"
		avlRange(edgeRoot, prefix, nodeUpperBound(node), func(k string, active bool) {
			parents = append(parents, pair{parent: k[len(prefix):], active: active})
		})
		sort.Slice(parents, func(i, j int) bool { return parents[i].parent < parents[j].parent })
		for _, par := range parents {
			if par.active && !visited[par.parent] {
				visited[par.parent] = true
				queue = append(queue, par.parent)
			}
			tr.Edges = append(tr.Edges, EdgeSnapshot{Parent: par.parent, Child: node, Active: par.active,
				Effective: effectiveEdge(edgeRoot, node, par.parent)})
		}
	}
	sortEdgeSnapshots(tr.Edges)
	sortGrantSnapshots(tr.Grants)
	return tr, true
}

type pair struct {
	parent string
	active bool
}

// effectiveEdge 从已解析的边树直接读取该边版本生效时刻。
func effectiveEdge(root *avlNode[bool], child, parent string) Timestamp {
	sv, ok := avlGet(root, edgeKey(parent, child))
	if !ok {
		return 0
	}
	return sv.at
}

// edgeKey 的物理键方向为 child-first，便于前缀区间查询。
func edgeKey(parent, child string) string { return child + "\x00" + parent }
func grantKey(node, typeID string) string { return node + "\x00" + typeID }

// nodeUpperBound 返回 child 前缀区间的严格上界：递增节点名最后一个非 0xff 字节。
// 例如 node="u2" -> "u3"，区间 ["u2\x00","u3") 覆盖任意父节点名。
func nodeUpperBound(node string) string {
	b := []byte(node)
	for i := len(b) - 1; i >= 0; i-- {
		if b[i] < 0xff {
			b[i]++
			return string(b)
		}
		b = b[:i]
	}
	return "\x00"
}

func sortEdgeSnapshots(s []EdgeSnapshot) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Parent != s[j].Parent {
			return s[i].Parent < s[j].Parent
		}
		return s[i].Child < s[j].Child
	})
}

func sortGrantSnapshots(s []GrantSnapshot) {
	sort.Slice(s, func(i, j int) bool {
		if s[i].Node != s[j].Node {
			return s[i].Node < s[j].Node
		}
		return s[i].TypeID < s[j].TypeID
	})
}
