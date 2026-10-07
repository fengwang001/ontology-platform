package ontology

import (
	"sort"
	"sync"
)

// Engine 是动作执行引擎。它用一把互斥锁把每次 Execute 串行化，
// 因此任意并发调用的可观察结果都等价于某个串行顺序；
// 被拒绝的动作在暂存区被整体丢弃，对后续动作不可见。
type Engine struct {
	mu    sync.Mutex
	store *Store
	auth  Authorizer
	decls map[ActionTypeID]ActionDecl
	log   DecisionLog
}

// NewEngine 构造引擎。
func NewEngine(store *Store, auth Authorizer) *Engine {
	return &Engine{store: store, auth: auth, decls: map[ActionTypeID]ActionDecl{}}
}

// RegisterAction 注册一个动作类型声明（管理态操作）。
func (e *Engine) RegisterAction(d ActionDecl) { e.decls[d.ID] = d }

// Store 返回底层存储（只读用途）。
func (e *Engine) Store() *Store { return e.store }

// Log 返回判定日志的拷贝。
func (e *Engine) Log() []DecisionRecord {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]DecisionRecord, len(e.log.Records))
	copy(out, e.log.Records)
	return out
}

// checkCounter 统计单次执行内的权限判定次数，
// 使开销上界成为可观测事实。
type checkCounter struct {
	auth    Authorizer
	n       int
	checked map[InstanceID]bool
}

func (c *checkCounter) visible(s SubjectID, id InstanceID) bool {
	c.n++
	c.checked[id] = true
	return c.auth.Visible(s, id)
}

func (c *checkCounter) authorize(s SubjectID, id InstanceID, d int) bool {
	c.n++
	c.checked[id] = true
	return c.auth.Authorize(s, id, d)
}

// Execute 校验并执行一次动作调用。
// 返回 allowed=false 且 err=nil 表示合并规则判定拒绝（非错误）；
// err 非空表示按固定优先顺序汇报的错误。
// 无论哪种拒绝，实例、链接、审计与时钟都不会发生任何变化。
func (e *Engine) Execute(subject SubjectID, inv Invocation) (bool, *ActionError) {
	e.mu.Lock()
	defer e.mu.Unlock()

	rec := DecisionRecord{Subject: subject, Inv: inv}
	counter := &checkCounter{auth: e.auth, checked: map[InstanceID]bool{}}
	finish := func(allowed bool, aerr *ActionError) (bool, *ActionError) {
		rec.Allowed = allowed
		if aerr != nil {
			rec.Err = aerr.Kind
		}
		rec.CheckCount = counter.n
		for id := range counter.checked {
			rec.Checked = append(rec.Checked, id)
		}
		sort.Slice(rec.Checked, func(i, j int) bool { return rec.Checked[i] < rec.Checked[j] })
		e.log.append(rec)
		return allowed, aerr
	}

	// 阶段 1：参数校验（ErrInvalidParams）。
	decl, ok := e.decls[inv.Action]
	if !ok {
		return finish(false, &ActionError{Kind: ErrInvalidParams, Detail: "unknown action type"})
	}
	if aerr := e.validate(decl, inv); aerr != nil {
		return finish(false, aerr)
	}

	// 阶段 2：结构遍历（不调用权限判定），检查传播深度上限。
	excluded := map[LinkTypeID]bool{}
	for _, lt := range decl.Cascade.ExcludeLinkTypes {
		excluded[lt] = true
	}
	targets := make([]InstanceID, 0, len(inv.Ops))
	for _, op := range inv.Ops {
		targets = append(targets, op.Target)
	}
	depth, path, aerr := e.propagate(decl, excluded, targets)
	rec.Path = path
	if aerr != nil {
		return finish(false, aerr)
	}

	isTarget := map[InstanceID]bool{}
	for _, t := range targets {
		isTarget[t] = true
	}

	// 阶段 3：直接目标可见性（ErrTargetInvisible）。
	for _, op := range inv.Ops {
		if op.Kind == OpCreate {
			continue // 新建实例尚不存在，无可见性问题
		}
		if !counter.visible(subject, op.Target) {
			return finish(false, &ActionError{Kind: ErrTargetInvisible, Target: op.Target, Detail: "direct target not visible to subject"})
		}
	}

	// 阶段 4：级联触及实例的可见性。
	var effectSet []InstanceID
	var skipped []SkipRecord
	for id := range depth {
		if isTarget[id] {
			continue
		}
		if counter.visible(subject, id) {
			effectSet = append(effectSet, id)
			continue
		}
		if decl.Invisible == InvisibleDeny {
			// 不携带实例标识，避免泄露其存在。
			return finish(false, &ActionError{Kind: ErrCascadeInvisible, Detail: "cascade reached an instance not visible to subject"})
		}
		skipped = append(skipped, SkipRecord{
			Instance: id,
			Reason:   "invisible",
			Impact:   "cascade touch not applied; properties, version and links unchanged",
		})
	}
	sort.Slice(skipped, func(i, j int) bool { return skipped[i].Instance < skipped[j].Instance })
	rec.Skipped = skipped
	// 排序保证执行结果与日志可确定性重放。
	sort.Slice(effectSet, func(i, j int) bool { return effectSet[i] < effectSet[j] })

	// 阶段 5：逐层授权结论的合并。AND/OR 折叠可交换可结合，
	// 结果与遍历各层级的顺序无关。
	grantSet := append([]InstanceID{}, targets...)
	grantSet = append(grantSet, effectSet...)
	allowed := false
	if decl.Merge == MergeAll {
		allowed = true
		for _, id := range grantSet {
			if !counter.authorize(subject, id, depth[id]) {
				allowed = false
				break
			}
		}
	} else {
		for _, id := range grantSet {
			if counter.authorize(subject, id, depth[id]) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return finish(false, nil)
	}

	// 阶段 6：暂存并原子提交。所有直接操作与被允许的级联影响
	// 作为同一个不可分割的整体生效。
	t := newTx()
	commitMark := e.store.Clock + 1
	for _, op := range inv.Ops {
		switch op.Kind {
		case OpCreate:
			props := make(map[string]string, len(op.Props))
			for k, v := range op.Props {
				props[k] = v
			}
			t.upserts[op.Target] = &Instance{ID: op.Target, Type: op.Type, Props: props, Version: 1, CascadeMark: commitMark}
			t.audit.Created = append(t.audit.Created, op.Target)
		case OpModify:
			in := t.get(e.store, op.Target).clone()
			for k, v := range op.Props {
				in.Props[k] = v
			}
			t.upserts[op.Target] = in
			t.audit.Modified = append(t.audit.Modified, op.Target)
		}
	}
	for _, id := range effectSet {
		in := t.get(e.store, id).clone()
		t.upserts[id] = in
		t.audit.Touched = append(t.audit.Touched, id)
	}
	for id, in := range t.upserts {
		in.CascadeMark = commitMark
		if !isCreateOp(inv, id) {
			in.Version++
		}
	}
	for _, sk := range skipped {
		t.audit.Skipped = append(t.audit.Skipped, sk.Instance)
	}
	e.store.commit(t)
	return finish(true, nil)
}

func isCreateOp(inv Invocation, id InstanceID) bool {
	for _, op := range inv.Ops {
		if op.Kind == OpCreate && op.Target == id {
			return true
		}
	}
	return false
}

// validate 执行全部参数校验，对应 ErrInvalidParams。
func (e *Engine) validate(decl ActionDecl, inv Invocation) *ActionError {
	bad := func(detail string) *ActionError {
		return &ActionError{Kind: ErrInvalidParams, Detail: detail}
	}
	if len(inv.Ops) == 0 {
		return bad("action declares no operations")
	}
	if decl.Cascade.MaxDepth < 0 {
		return bad("negative cascade max depth")
	}
	for _, lt := range decl.Cascade.ExcludeLinkTypes {
		if _, ok := e.store.LinkTypes[lt]; !ok {
			return bad("excluded link type not registered: " + string(lt))
		}
	}
	created := map[InstanceID]bool{}
	for _, op := range inv.Ops {
		if op.Kind != OpCreate && op.Kind != OpModify {
			return bad("unknown operation kind")
		}
		ot, ok := e.store.Types[op.Type]
		if !ok {
			return bad("unknown object type: " + string(op.Type))
		}
		if !decl.allows(op.Type, op.Kind) {
			return bad("operation not declared for object type: " + string(op.Type))
		}
		if op.Target == "" {
			return bad("empty target id")
		}
		for p := range op.Props {
			if !ot.HasProperty(p) {
				return bad("unknown property " + p + " for object type " + string(op.Type))
			}
		}
		switch op.Kind {
		case OpCreate:
			if _, exists := e.store.Instances[op.Target]; exists {
				return bad("create target already exists: " + string(op.Target))
			}
			if created[op.Target] {
				return bad("duplicate create target: " + string(op.Target))
			}
			created[op.Target] = true
		case OpModify:
			in, exists := e.store.Instances[op.Target]
			if !exists {
				return bad("modify target does not exist: " + string(op.Target))
			}
			if in.Type != op.Type {
				return bad("modify target type mismatch: " + string(op.Target))
			}
		}
	}
	return nil
}

// propagate 从直接目标出发做广度优先结构遍历。
// visited 集合保证环上每个实例只被访问一次、遍历必然终止。
// 返回每个实例的深度、有序路径记录；若关系图要求超过声明上限的
// 传播，返回 ErrDepthExceeded。
func (e *Engine) propagate(decl ActionDecl, excluded map[LinkTypeID]bool, targets []InstanceID) (map[InstanceID]int, []PathEntry, *ActionError) {
	depth := map[InstanceID]int{}
	var path []PathEntry
	frontier := make([]InstanceID, 0, len(targets))
	for _, t := range targets {
		if _, seen := depth[t]; !seen {
			depth[t] = 0
			frontier = append(frontier, t)
			path = append(path, PathEntry{Instance: t, Depth: 0})
		}
	}
	for d := 1; d <= decl.Cascade.MaxDepth; d++ {
		var next []InstanceID
		for _, id := range frontier {
			for _, l := range e.sortedNeighbors(id, excluded) {
				if _, seen := depth[l.To]; seen {
					continue
				}
				depth[l.To] = d
				next = append(next, l.To)
				path = append(path, PathEntry{Instance: l.To, Depth: d, ViaLink: l.Type})
			}
		}
		frontier = next
	}
	// 严格深度语义：若 frontier 仍有通向未访问实例的边，
	// 说明完整级联影响需要超过声明上限的传播深度。
	for _, id := range frontier {
		for _, l := range e.sortedNeighbors(id, excluded) {
			if _, seen := depth[l.To]; !seen {
				return nil, path, &ActionError{Kind: ErrDepthExceeded, Detail: "propagation requires depth beyond declared max"}
			}
		}
	}
	return depth, path, nil
}

func (e *Engine) sortedNeighbors(id InstanceID, excluded map[LinkTypeID]bool) []Link {
	ls := e.store.neighbors(id, excluded)
	sort.Slice(ls, func(i, j int) bool {
		if ls[i].Type != ls[j].Type {
			return ls[i].Type < ls[j].Type
		}
		return ls[i].To < ls[j].To
	})
	return ls
}
