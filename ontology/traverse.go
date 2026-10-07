package ontology

import "sort"

// DefaultPageSize 是单个分页请求返回对象数的内部上限（接口未对外暴露页大小参数）。
const DefaultPageSize = 64

// Page 是一次 Traverse 请求的结果页。
type Page struct {
	// Objects：本页对象，按固定的前序 DFS 遍历顺序排列（兄弟节点按对象标识升序）。
	Objects []Object
	// NextToken：下一页续读标记；Done=true 时为空。
	NextToken string
	// Done：是否已把该遍历在深度与扇出限制下的全部对象分页返回完。
	Done bool
	// Truncation：三态截断标记。非最终页给出“截至当前已确认”的最佳值；
	// 最终页的值是确定性结论（complete / fanout_truncated / depth_truncated）。
	Truncation Truncation

	metrics Metrics
}

// Metrics 返回本次请求实际访问规模的内部度量（仅供验证，不属于对外语义契约）。
func (p *Page) Metrics() Metrics { return p.metrics }

// Iterator 是绑定单个 Store 与权限模型的分页遍历器。
type Iterator struct {
	store    *Store
	perms    PermissionModel
	codec    *tokenCodec
	pageSize int
}

func NewIterator(store *Store, perms PermissionModel) *Iterator {
	return &Iterator{store: store, perms: perms, codec: newTokenCodec(), pageSize: DefaultPageSize}
}

// SetPageSize 仅供测试缩小页大小以覆盖分页路径。
func (it *Iterator) SetPageSize(n int) {
	if n > 0 {
		it.pageSize = n
	}
}

// Traverse 按深度与单节点扇出两个独立上限返回起点的邻域分页。
//
// 拒绝判定次序：
// 参数非法 > 起点无存在性权限（受限） > 续读锚定起点已不存在（标记失效） > 正常分页。
func (it *Iterator) Traverse(start string, maxDepth, maxFanout int, token string, actor Actor) (*Page, error) {
	// 1. 参数非法。
	if maxDepth < 0 || maxFanout < 0 || start == "" {
		return nil, ErrInvalidArgument
	}

	var ct *continuationToken
	if token != "" {
		t, err := it.codec.decode(token)
		if err != nil {
			return nil, err
		}
		ct = t
	}

	var snap *snapshotHandle
	current := it.store.Current()
	if ct != nil {
		// 起点对象的“当前是否仍存在”是标记可追溯性的判据，必须看当前状态
		// 而非锚定快照（快照天然保留历史，否则标记永远不会失效）。
		if _, alive := current.object(ct.Start); !alive {
			return nil, ErrTokenObsolete
		}
		// 续读锚定在标记产生时的快照版本；之后的增删对本次遍历不可见。
		h, ok := it.store.Get(ct.Version)
		if !ok {
			return nil, ErrTokenObsolete
		}
		snap = h
		start = ct.Start
	} else {
		snap = current
	}

	// 2/3. 起点存在性：先判断对象在快照中是否存在。
	_, exists := snap.object(start)
	if !exists {
		if ct != nil {
			// 3. 续读标记锚定的起点在当前状态下已不存在。
			return nil, ErrTokenObsolete
		}
		// 首次请求对不存在的起点：无存在性权限 -> 受限不可遍历。
		return nil, ErrForbidden
	}
	// 2. 起点对象存在但调用者无存在性权限。
	if !it.perms.CanSee(snap, actor, start) {
		return nil, ErrForbidden
	}

	if ct == nil {
		ct = &continuationToken{
			Version:    snap.Version(),
			Start:      start,
			MaxDepth:   maxDepth,
			Fanout:     maxFanout,
			PageSize:   it.pageSize,
			Stack:      []frame{{Node: start, Depth: 0}},
			Excluded:   map[string]struct{}{},
			EmittedSet: map[string]struct{}{},
		}
	}

	run := &runner{
		it:    it,
		snap:  snap.forkMetrics(),
		actor: actor,
		ct:    ct,
	}
	page := &Page{Objects: []Object{}}

	for len(ct.Stack) > 0 && len(page.Objects) < ct.PageSize {
		top := len(ct.Stack) - 1
		fr := ct.Stack[top]
		ct.Stack = ct.Stack[:top]

		// 被其他父节点的扇出截断排除的对象不得在此页或后续任何页复活。
		if _, dropped := ct.Excluded[fr.Node]; dropped {
			continue
		}
		// 已发射过的重复栈帧跳过（同一对象只在固定顺序中首次访问处出现）。
		if _, emitted := ct.EmittedSet[fr.Node]; emitted {
			continue
		}
		obj, ok := run.snap.object(fr.Node)
		if !ok {
			continue
		}
		page.Objects = append(page.Objects, *obj)
		ct.Emitted++
		ct.EmittedSet[fr.Node] = struct{}{}

		if fr.Depth >= ct.MaxDepth {
			// 自身已返回；仅当确实存在本可遍历的直接后继时才构成深度截断。
			if run.hasAnySuccessor(fr.Node) && ct.Trunc != TruncFanout {
				ct.Trunc = TruncDepth
			}
			continue
		}

		targets, fanoutHit := run.expand(fr.Node)
		if fanoutHit {
			// 扇出优先级高于深度：一旦确认扇出截断，覆盖深度结论。
			ct.Trunc = TruncFanout
		}
		// 超出扇出上限的兄弟整体记入 Excluded，续读标记不得将其重新纳入。
		if len(targets) > ct.Fanout {
			for _, t := range targets[ct.Fanout:] {
				ct.Excluded[t] = struct{}{}
			}
			targets = targets[:ct.Fanout]
		}
		// 压栈逆序，保证弹栈顺序 = 目标标识升序。
		for i := len(targets) - 1; i >= 0; i-- {
			ct.Stack = append(ct.Stack, frame{Node: targets[i], Depth: fr.Depth + 1})
		}
	}

	page.Truncation = ct.Trunc
	page.metrics = *run.snap.metrics
	if len(ct.Stack) == 0 {
		page.Done = true
		page.NextToken = ""
	} else {
		page.Done = false
		page.NextToken = it.codec.encode(ct)
	}
	return page, nil
}

type runner struct {
	it    *Iterator
	snap  *snapshotHandle
	actor Actor
	ct    *continuationToken
}

// expand 返回该对象经权限过滤、去重、按对象标识升序后的直接后继；
// 权限过滤先于扇出判定：不可见对象与无遍历权限链接不占用扇出名额。
func (r *runner) expand(node string) (targets []string, fanoutHit bool) {
	edges := r.snap.outgoing(node)
	eligible := make(map[string]struct{}, len(edges))
	for _, key := range edges {
		link := Link{Type: key.typ, Source: key.source, Target: key.target}
		if _, ok := r.snap.linkType(key.typ); !ok {
			continue
		}
		if !r.it.perms.CanTraverse(r.snap, r.actor, link) {
			continue
		}
		if !r.it.perms.CanSee(r.snap, r.actor, key.target) {
			continue
		}
		if _, exists := r.snap.object(key.target); !exists {
			continue
		}
		eligible[key.target] = struct{}{}
	}

	out := make([]string, 0, len(eligible))
	for t := range eligible {
		if _, dropped := r.ct.Excluded[t]; dropped {
			continue
		}
		if _, emitted := r.ct.EmittedSet[t]; emitted {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)

	// 扇出上限在权限过滤、去重之后应用。
	fanoutHit = len(out) > r.ct.Fanout
	return out, fanoutHit
}

// hasAnySuccessor 判断该对象在无深度限制假设下是否存在任意可遍历、
// 目标存在且可见的直接后继（用于深度截断三态判定）。
func (r *runner) hasAnySuccessor(node string) bool {
	for _, key := range r.snap.outgoing(node) {
		link := Link{Type: key.typ, Source: key.source, Target: key.target}
		if _, ok := r.snap.linkType(key.typ); !ok {
			continue
		}
		if !r.it.perms.CanTraverse(r.snap, r.actor, link) {
			continue
		}
		if _, dropped := r.ct.Excluded[key.target]; dropped {
			continue
		}
		if _, emitted := r.ct.EmittedSet[key.target]; emitted {
			continue
		}
		if !r.it.perms.CanSee(r.snap, r.actor, key.target) {
			continue
		}
		if _, exists := r.snap.object(key.target); !exists {
			continue
		}
		return true
	}
	return false
}
