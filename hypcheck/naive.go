package hypcheck

import "sort"

// NaiveReplay 是独立实现的朴素重演模型：
// 每次预检从日志起点线性扫描全部事件（O(全部事件数)），重建 at 时刻的
// 动作类型、钩子集合、权限继承图与对象状态后直接判定。
//
// 它刻意不使用引擎的 AVL 快照、二分 as-of 与任何缓存，作为差分测试基准；
// 与引擎唯一共享的是事件定义、判定语义函数（validateParams/runPre/...）
// 与错误优先级规定——这些属于“需求本身”，双方独立构建状态。
type NaiveReplay struct {
	events []Event
	// 压实后的清册（若存在）。
	manifest *Manifest
	horizon  Timestamp
}

func NewNaiveReplay() *NaiveReplay { return &NaiveReplay{} }

// LoadEvents 用完整事件序列初始化（测试中直接喂入同一日志的导出副本）。
func (n *NaiveReplay) LoadEvents(events []Event, m *Manifest) {
	n.events = append([]Event(nil), events...)
	n.manifest = m
	if m != nil {
		n.horizon = m.Horizon
	}
}

// naiveWorld 是线性扫描后在 at 时刻的朴素世界状态。
type naiveWorld struct {
	typeAt    map[string]Timestamp
	types     map[string]TypeVersion
	hookPubAt map[string]Timestamp
	hooks     map[string]HookVersion
	bindAt    map[string]Timestamp
	bindings  map[string]bindingSet
	nodeAt    map[string]Timestamp
	nodes     map[string]bool
	edgeAt    map[string]Timestamp
	edges     map[string]bool
	grantAt   map[string]Timestamp
	grants    map[string]bool
	stateAt   map[string]Timestamp
	state     map[string]Value
}

func newNaiveWorld() *naiveWorld {
	return &naiveWorld{
		typeAt: map[string]Timestamp{}, types: map[string]TypeVersion{},
		hookPubAt: map[string]Timestamp{}, hooks: map[string]HookVersion{},
		bindAt: map[string]Timestamp{}, bindings: map[string]bindingSet{},
		nodeAt: map[string]Timestamp{}, nodes: map[string]bool{},
		edgeAt: map[string]Timestamp{}, edges: map[string]bool{},
		grantAt: map[string]Timestamp{}, grants: map[string]bool{},
		stateAt: map[string]Timestamp{}, state: map[string]Value{},
	}
}

func (n *NaiveReplay) buildWorld(at Timestamp) *naiveWorld {
	w := newNaiveWorld()
	if n.manifest != nil && at >= n.horizon {
		for k, b := range n.manifest.TypeValues {
			w.typeAt[k] = b.At
			w.types[k] = b.TV
		}
		for k, b := range n.manifest.Hooks {
			w.hookPubAt[k] = b.At
			w.hooks[k] = b.HV
		}
		for k, b := range n.manifest.Bindings {
			w.bindAt[k] = b.At
			w.bindings[k] = bindingSet{phase: b.Phase}
		}
		for k, b := range n.manifest.Edges {
			w.edgeAt[k] = b.At
			w.edges[k] = b.V
		}
		for k, b := range n.manifest.Grants {
			w.grantAt[k] = b.At
			w.grants[k] = b.V
		}
		for k, b := range n.manifest.State {
			w.stateAt[k] = b.At
			w.state[k] = b.V
		}
		for k, rec := range n.manifest.Principals {
			w.nodeAt[k] = rec.BornAt
			w.nodes[k] = rec.Existed
		}
	}
	for _, ev := range n.events {
		if ev.At > at {
			break
		}
		switch ev.Kind {
		case evTypeDefined:
			w.typeAt[ev.TypeID] = ev.At
			w.types[ev.TypeID] = *ev.TV
		case evHookPublished:
			w.hookPubAt[ev.HookID] = ev.At
			w.hooks[ev.HookID] = *ev.HV
		case evHookBound:
			k := bindingKey(ev.TypeID, ev.HookID)
			w.bindAt[k] = ev.At
			w.bindings[k] = bindingSet{phase: ev.Phase}
		case evPrincipal:
			w.nodeAt[ev.Principal] = ev.At
			w.nodes[ev.Principal] = *ev.Exists
		case evEdgeToggled:
			k := edgeKey(ev.Parent, ev.Child)
			w.edgeAt[k] = ev.At
			w.edges[k] = *ev.Granted
		case evGrantToggled:
			k := grantKey(ev.Node, ev.TypeID)
			w.grantAt[k] = ev.At
			w.grants[k] = *ev.Granted
		case evStateWritten:
			w.stateAt[ev.Key] = ev.At
			w.state[ev.Key] = *ev.Value
		}
	}
	return w
}

// Precheck 朴素重演一次历史预检，返回与引擎同构的裁决。
func (n *NaiveReplay) Precheck(req PrecheckRequest) *PrecheckResult {
	res := &PrecheckResult{At: req.At, LinearSeq: Seq(len(n.events))}
	fail := func(class ErrorClass, code, msg string) *PrecheckResult {
		res.Verdict = VerdictError
		res.ErrorClass = class
		res.ErrorCode = code
		res.Message = msg
		return res
	}

	if req.At < n.horizon {
		rec, known := n.manifest.Types[req.TypeID]
		if !known || rec.BornAt >= n.horizon {
			return fail(ErrHistoryGap, "compact_horizon", "naive: gap")
		}
		return fail(ErrTypeUndefined, "type_not_yet_defined", "naive: undefined")
	}

	w := n.buildWorld(req.At)

	tv, tExists := w.types[req.TypeID]
	if !tExists {
		return fail(ErrTypeUndefined, "type_not_yet_defined", "naive: undefined")
	}
	res.TypeVersion = versionLabel(w.typeAt[req.TypeID])

	// 钩子集合（与引擎相同的排序 / 覆盖规则）。
	prefix := req.TypeID + "\x01"
	var ids []string
	phaseOf := map[string]Phase{}
	for k, b := range w.bindings {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix && b.phase != "" {
			id := k[len(prefix):]
			ids = append(ids, id)
			phaseOf[id] = b.phase
		}
	}
	sort.Strings(ids)
	var resolved []ResolvedHook
	for _, id := range ids {
		hv, ok := w.hooks[id]
		if !ok {
			continue
		}
		resolved = append(resolved, ResolvedHook{HookID: id, Phase: phaseOf[id], Version: hv.Version, Spec: hv.Spec})
	}
	res.Hooks = resolved

	exists, knownNode := w.nodes[req.Caller]
	if !knownNode {
		return fail(ErrCallerUnknown, "caller_not_yet_existing", "naive: caller unknown")
	}
	if !exists {
		return fail(ErrCallerUnknown, "caller_not_yet_existing", "naive: caller unknown")
	}

	if errs := validateParams(tv.Schema, req.Params); len(errs) > 0 {
		return fail(ErrBadParams, "schema_violation", "naive: bad params")
	}

	trace := n.naivePerm(w, req.Caller, req.TypeID)
	res.PermTrace = trace

	fs := &frozenState{vals: map[string]Value{}}
	for _, k := range req.ObjectKeys {
		if v, ok := w.state[k]; ok {
			fs.vals[k] = v
		}
	}

	preFails := runPre(resolved, trace, fs, req.Params, req.CollectAll)
	res.PreFailures = preFails
	if len(preFails) > 0 {
		res.Verdict = VerdictDenied
		return res
	}
	intended := renderEffects(tv, req.Params)
	postFails := runPost(resolved, fs, req.Params, intended, req.CollectAll)
	res.PostFailures = postFails
	if len(postFails) > 0 {
		res.Verdict = VerdictDenied
		return res
	}
	res.Verdict = VerdictAllowed
	res.Effects = intended
	return res
}

// naivePerm 用朴素邻接表 BFS 重建权限快照（独立于引擎的 permState）。
func (n *NaiveReplay) naivePerm(w *naiveWorld, caller, typeID string) *PermTrace {
	tr := &PermTrace{Caller: caller}
	visited := map[string]bool{caller: true}
	queue := []string{caller}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		tr.Ancestors = append(tr.Ancestors, node)

		gk := grantKey(node, typeID)
		granted := w.grants[gk]
		tr.Grants = append(tr.Grants, GrantSnapshot{Node: node, TypeID: typeID, Granted: granted, Effective: w.grantAt[gk]})
		if granted && !tr.Allowed {
			tr.Allowed = true
			tr.GrantedBy = node
		}

		var parents []pair
		prefix := node + "\x00"
		for k, active := range w.edges {
			if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
				parents = append(parents, pair{parent: k[len(prefix):], active: active})
			}
		}
		sort.Slice(parents, func(i, j int) bool { return parents[i].parent < parents[j].parent })
		for _, p := range parents {
			ek := edgeKey(p.parent, node)
			tr.Edges = append(tr.Edges, EdgeSnapshot{Parent: p.parent, Child: node, Active: p.active, Effective: w.edgeAt[ek]})
			if p.active && !visited[p.parent] {
				visited[p.parent] = true
				queue = append(queue, p.parent)
			}
		}
	}
	sortEdgeSnapshots(tr.Edges)
	sortGrantSnapshots(tr.Grants)
	return tr
}
