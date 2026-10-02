package acl

// Effective 返回 E(node) 的拷贝。
func (s *Store) Effective(nodeID string) ([]EffectiveEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, ok := s.nodes[nodeID]
	if !ok {
		return nil, newError(ErrNotFound, "node %q does not exist", nodeID)
	}
	eff, _ := s.effectiveLocked(n)
	return eff, nil
}

// Eval 按已授予位累计的方式判定访问请求。
func (s *Store) Eval(token []string, nodeID string, R uint32) (EvalResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(token) == 0 {
		return EvalResult{}, newError(ErrInvalid, "token is empty")
	}
	for _, p := range token {
		if p == "" {
			return EvalResult{}, newError(ErrInvalid, "token contains an empty principal")
		}
	}
	if R < 1 || R > maxMask {
		return EvalResult{}, newError(ErrInvalid, "requested mask %d out of range [1, %d]", R, maxMask)
	}
	n, ok := s.nodes[nodeID]
	if !ok {
		return EvalResult{}, newError(ErrNotFound, "node %q does not exist", nodeID)
	}
	eff, visited := s.effectiveLocked(n)
	s.lastEvalNodes = visited

	inToken := make(map[string]struct{}, len(token))
	for _, p := range token {
		inToken[p] = struct{}{}
	}
	var G uint32
	processed := 0
	for i, e := range eff {
		processed++
		if e.Flags&FlagIO != 0 {
			continue
		}
		if _, ok := inToken[e.Principal]; !ok {
			continue
		}
		if !e.Allow {
			if e.Mask&R&^G != 0 {
				s.lastEvalEntries = processed
				return EvalResult{
					Allowed:           false,
					Kind:              ResultDeniedHit,
					DecisiveIndex:     i,
					DecisiveSource:    e.Source,
					DecisiveInherited: e.Source != nodeID,
					Granted:           G,
				}, nil
			}
			continue
		}
		G |= e.Mask & R
		if G == R {
			s.lastEvalEntries = processed
			return EvalResult{
				Allowed:           true,
				Kind:              ResultGranted,
				DecisiveIndex:     i,
				DecisiveSource:    e.Source,
				DecisiveInherited: e.Source != nodeID,
				Granted:           G,
			}, nil
		}
	}
	s.lastEvalEntries = processed
	return EvalResult{
		Allowed:       false,
		Kind:          ResultImplicitDeny,
		DecisiveIndex: -1,
		Granted:       G,
	}, nil
}

// effectiveLocked 计算 E(n)，并返回构造过程中读取过显式列表的节点数。
// 从 n 起沿祖先链上行到最近的保护节点（含）或根为止。
func (s *Store) effectiveLocked(n *node) ([]EffectiveEntry, int) {
	chain := []*node{}
	for cur := n; ; cur = cur.parent {
		chain = append(chain, cur)
		if cur.protected || cur.parent == nil {
			break
		}
	}
	eff := explicitOrdered(chain[len(chain)-1])
	for i := len(chain) - 2; i >= 0; i-- {
		child := chain[i]
		next := explicitOrdered(child)
		next = append(next, transform(eff, child.container)...)
		eff = next
	}
	return eff, len(chain)
}

// explicitOrdered 即 X(n)：全部拒绝条目（保持原相对顺序）后接全部允许条目。
func explicitOrdered(n *node) []EffectiveEntry {
	out := make([]EffectiveEntry, 0, len(n.aces))
	for pass := 0; pass < 2; pass++ {
		allow := pass == 1
		for _, a := range n.aces {
			if a.Allow == allow {
				out = append(out, EffectiveEntry{
					Allow:     a.Allow,
					Principal: a.Principal,
					Mask:      a.Mask,
					Flags:     a.Flags,
					Source:    n.id,
				})
			}
		}
	}
	return out
}

// transform 即 T：把父有效列表按子节点是否为容器逐条变换，保持父列表顺序。
func transform(parent []EffectiveEntry, childContainer bool) []EffectiveEntry {
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
