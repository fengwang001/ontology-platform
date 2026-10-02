package ontology

func cloneEffectiveACE(ace EffectiveACE) EffectiveACE {
	ace.Principal = cloneBytes(ace.Principal)
	ace.Source = cloneBytes(ace.Source)
	return ace
}

func cloneEffectiveList(aces []EffectiveACE) []EffectiveACE {
	result := make([]EffectiveACE, len(aces))
	for i, ace := range aces {
		result[i] = cloneEffectiveACE(ace)
	}
	return result
}

func inheritFlags(flags uint8, childContainer bool) (uint8, bool) {
	if childContainer {
		if flags&FlagCI != 0 {
			if flags&FlagNP != 0 {
				return 0, true
			}
			return flags & (FlagOI | FlagCI), true
		}
		if flags&FlagOI != 0 {
			if flags&FlagNP != 0 {
				return 0, false
			}
			return FlagOI | FlagIO, true
		}
		return 0, false
	}

	if flags&FlagOI == 0 {
		return 0, false
	}
	return 0, true
}

func (a *ACL) buildEffectiveLocked(target *node, countVisit bool) []EffectiveACE {
	chain := make([]*node, 0, target.depth+1)
	for current := target; ; {
		chain = append(chain, current)
		if current.id == RootID || current.protected {
			break
		}
		current = a.nodes[current.parent]
	}

	if countVisit {
		a.evalNodesVisited.Add(uint64(len(chain)))
	}

	var effective []EffectiveACE
	for i := len(chain) - 1; i >= 0; i-- {
		current := chain[i]
		next := cloneEffectiveList(current.aces)
		if current.id == RootID || current.protected || i == len(chain)-1 {
			effective = next
			continue
		}

		for _, ace := range effective {
			flags, ok := inheritFlags(ace.Flags, current.container)
			if !ok {
				continue
			}
			copyACE := cloneEffectiveACE(ace)
			copyACE.Flags = flags
			next = append(next, copyACE)
		}
		effective = next
	}
	return effective
}

func (a *ACL) Effective(node []byte) ([]EffectiveACE, error) {
	if len(node) == 0 {
		return nil, newACLError("Effective", ErrInvalidArgument)
	}

	a.mu.RLock()
	target, ok := a.nodes[string(node)]
	if !ok {
		a.mu.RUnlock()
		return nil, newACLError("Effective", ErrNotFound)
	}
	effective := a.buildEffectiveLocked(target, false)
	a.mu.RUnlock()
	return effective, nil
}

func (a *ACL) Eval(token [][]byte, node []byte, request uint16) (Decision, error) {
	if len(token) == 0 || len(node) == 0 || request == 0 {
		return Decision{}, newACLError("Eval", ErrInvalidArgument)
	}
	principals := make(map[string]struct{}, len(token))
	for _, principal := range token {
		if len(principal) == 0 {
			return Decision{}, newACLError("Eval", ErrInvalidArgument)
		}
		principals[string(principal)] = struct{}{}
	}

	a.mu.RLock()
	target, ok := a.nodes[string(node)]
	if !ok {
		a.mu.RUnlock()
		return Decision{}, newACLError("Eval", ErrNotFound)
	}
	effective := a.buildEffectiveLocked(target, true)
	a.mu.RUnlock()

	nodeID := string(node)
	var granted uint16
	for i, ace := range effective {
		a.evalEntriesProcessed.Add(1)
		if ace.Flags&FlagIO != 0 {
			continue
		}
		if _, ok := principals[string(ace.Principal)]; !ok {
			continue
		}

		if ace.Allow {
			granted |= ace.Mask & request
			if granted == request {
				return Decision{
					Allowed:       true,
					Result:        ResultGrant,
					DecisiveIndex: i,
					Source:        cloneBytes(ace.Source),
					Inherited:     string(ace.Source) != nodeID,
					Granted:       granted,
				}, nil
			}
			continue
		}

		if ace.Mask&request&^granted != 0 {
			return Decision{
				Allowed:       false,
				Result:        ResultDenyHit,
				DecisiveIndex: i,
				Source:        cloneBytes(ace.Source),
				Inherited:     string(ace.Source) != nodeID,
				Granted:       granted,
			}, nil
		}
	}

	return Decision{
		Allowed:       false,
		Result:        ResultImplicitDeny,
		DecisiveIndex: -1,
		Source:        nil,
		Inherited:     false,
		Granted:       granted,
	}, nil
}
