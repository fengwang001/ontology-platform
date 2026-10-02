package ontology

func newACLError(op string, kind error) *Error {
	return &Error{Kind: kind.Error(), Op: op, Err: kind}
}

func validACE(ace ACE) bool {
	if len(ace.Principal) == 0 || ace.Mask == 0 || ace.Flags > FlagOI|FlagCI|FlagNP|FlagIO {
		return false
	}
	if (ace.Flags&(FlagNP|FlagIO) != 0) && ace.Flags&(FlagOI|FlagCI) == 0 {
		return false
	}
	return true
}

func cloneBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func explicitACEs(id string, aces []ACE) []EffectiveACE {
	result := make([]EffectiveACE, 0, len(aces))
	for sourceIndex, ace := range aces {
		if !ace.Allow {
			result = append(result, EffectiveACE{
				ACE: ACE{
					Allow:     ace.Allow,
					Principal: cloneBytes(ace.Principal),
					Mask:      ace.Mask,
					Flags:     ace.Flags,
				},
				Source: []byte(id),
				Index:  sourceIndex,
			})
		}
	}
	for sourceIndex, ace := range aces {
		if ace.Allow {
			result = append(result, EffectiveACE{
				ACE: ACE{
					Allow:     ace.Allow,
					Principal: cloneBytes(ace.Principal),
					Mask:      ace.Mask,
					Flags:     ace.Flags,
				},
				Source: []byte(id),
				Index:  sourceIndex,
			})
		}
	}
	return result
}

func (a *ACL) AddNode(id []byte, parent []byte, container bool) error {
	if len(id) == 0 || len(parent) == 0 {
		return newACLError("AddNode", ErrInvalidArgument)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	parentNode, ok := a.nodes[string(parent)]
	if !ok {
		return newACLError("AddNode", ErrNotFound)
	}
	idString := string(id)
	if _, exists := a.nodes[idString]; exists {
		return newACLError("AddNode", ErrConflict)
	}
	if !parentNode.container {
		return newACLError("AddNode", ErrConflict)
	}
	if parentNode.depth+1 > a.depthMax {
		return newACLError("AddNode", ErrLimitExceeded)
	}

	a.nodes[idString] = &node{
		id:        idString,
		parent:    parentNode.id,
		children:  make(map[string]struct{}),
		container: container,
		depth:     parentNode.depth + 1,
	}
	parentNode.children[idString] = struct{}{}
	a.version++
	return nil
}

func (a *ACL) SetACL(node []byte, aces []ACE, protected bool) error {
	if len(node) == 0 {
		return newACLError("SetACL", ErrInvalidArgument)
	}
	for _, ace := range aces {
		if !validACE(ace) {
			return newACLError("SetACL", ErrInvalidArgument)
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	target, ok := a.nodes[string(node)]
	if !ok {
		return newACLError("SetACL", ErrNotFound)
	}
	if len(aces) > a.entryMax {
		return newACLError("SetACL", ErrLimitExceeded)
	}

	target.aces = explicitACEs(target.id, aces)
	target.protected = protected
	a.version++
	return nil
}

func (a *ACL) isDescendantLocked(ancestorID, descendantID string) bool {
	current := descendantID
	for current != "" {
		if current == ancestorID {
			return true
		}
		current = a.nodes[current].parent
	}
	return false
}

func (a *ACL) subtreeMaxDepthLocked(rootID string) int {
	maxDepth := a.nodes[rootID].depth
	var walk func(string)
	walk = func(id string) {
		current := a.nodes[id]
		if current.depth > maxDepth {
			maxDepth = current.depth
		}
		for childID := range current.children {
			walk(childID)
		}
	}
	walk(rootID)
	return maxDepth
}

func (a *ACL) Move(node []byte, newParent []byte) error {
	if len(node) == 0 || len(newParent) == 0 {
		return newACLError("Move", ErrInvalidArgument)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	target, ok := a.nodes[string(node)]
	if !ok {
		return newACLError("Move", ErrNotFound)
	}
	destination, parentExists := a.nodes[string(newParent)]
	if !parentExists {
		return newACLError("Move", ErrNotFound)
	}

	if target.id == RootID {
		return newACLError("Move", ErrConflict)
	}
	if !destination.container {
		return newACLError("Move", ErrConflict)
	}
	if destination.id == target.id || a.isDescendantLocked(target.id, destination.id) {
		return newACLError("Move", ErrConflict)
	}
	if destination.id == target.parent {
		return newACLError("Move", ErrConflict)
	}

	currentMaxDepth := a.subtreeMaxDepthLocked(target.id)
	projectedMaxDepth := destination.depth + 1 + (currentMaxDepth - target.depth)
	if projectedMaxDepth > a.depthMax {
		return newACLError("Move", ErrLimitExceeded)
	}

	oldParent := a.nodes[target.parent]
	delete(oldParent.children, target.id)
	target.parent = destination.id
	destination.children[target.id] = struct{}{}

	depthDelta := destination.depth + 1 - target.depth
	var updateDepth func(string)
	updateDepth = func(id string) {
		current := a.nodes[id]
		current.depth += depthDelta
		for childID := range current.children {
			updateDepth(childID)
		}
	}
	updateDepth(target.id)

	a.version++
	return nil
}
