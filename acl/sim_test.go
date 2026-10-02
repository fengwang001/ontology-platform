package acl

// 朴素模拟：严格按定义递归重算 E(n)，用于对照 Store 的求值结果。

type simNode struct {
	id        string
	parent    *simNode
	container bool
	aces      []ACE
	protected bool
}

type sim struct {
	nodes map[string]*simNode
}

func newSim() *sim {
	root := &simNode{id: "/", container: true}
	return &sim{nodes: map[string]*simNode{"/": root}}
}

func (sm *sim) add(id, parent string, container bool) {
	sm.nodes[id] = &simNode{id: id, parent: sm.nodes[parent], container: container}
}

func (sm *sim) setACL(id string, aces []ACE, protected bool) {
	cp := make([]ACE, len(aces))
	copy(cp, aces)
	n := sm.nodes[id]
	n.aces = cp
	n.protected = protected
}

func (sm *sim) move(id, newParent string) {
	sm.nodes[id].parent = sm.nodes[newParent]
}

// simX 即 X(n)：拒绝（保持相对顺序）后接允许（保持相对顺序）。
func simX(id string, n *simNode) []EffectiveEntry {
	out := make([]EffectiveEntry, 0, len(n.aces))
	for pass := 0; pass < 2; pass++ {
		allow := pass == 1
		for _, a := range n.aces {
			if a.Allow == allow {
				out = append(out, EffectiveEntry{a.Allow, a.Principal, a.Mask, a.Flags, id})
			}
		}
	}
	return out
}

// simT 即变换 T：保持父列表顺序逐条变换。
func simT(parent []EffectiveEntry, childContainer bool) []EffectiveEntry {
	out := make([]EffectiveEntry, 0, len(parent))
	for _, e := range parent {
		f := e.Flags
		if !childContainer {
			if f&FlagOI != 0 {
				e.Flags = 0
				out = append(out, e)
			}
			continue
		}
		switch {
		case f&FlagCI != 0:
			if f&FlagNP != 0 {
				e.Flags = 0
			} else {
				e.Flags = f & (FlagOI | FlagCI)
			}
			out = append(out, e)
		case f&FlagOI != 0:
			if f&FlagNP == 0 {
				e.Flags = FlagOI | FlagIO
				out = append(out, e)
			}
		}
	}
	return out
}

// effective 按定义递归：E(n)=X(n) 后接 T(E(父))，保护或根时后一段为空。
func (sm *sim) effective(id string) []EffectiveEntry {
	n := sm.nodes[id]
	out := simX(id, n)
	if n.parent != nil && !n.protected {
		out = append(out, simT(sm.effective(n.parent.id), n.container)...)
	}
	return out
}

// eval 按已授予位累计规则判定。
func (sm *sim) eval(token []string, id string, R uint32) EvalResult {
	eff := sm.effective(id)
	inToken := make(map[string]struct{}, len(token))
	for _, p := range token {
		inToken[p] = struct{}{}
	}
	var G uint32
	for i, e := range eff {
		if e.Flags&FlagIO != 0 {
			continue
		}
		if _, ok := inToken[e.Principal]; !ok {
			continue
		}
		if !e.Allow {
			if e.Mask&R&^G != 0 {
				return EvalResult{false, ResultDeniedHit, i, e.Source, e.Source != id, G}
			}
			continue
		}
		G |= e.Mask & R
		if G == R {
			return EvalResult{true, ResultGranted, i, e.Source, e.Source != id, G}
		}
	}
	return EvalResult{false, ResultImplicitDeny, -1, "", false, G}
}
