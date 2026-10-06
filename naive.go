package incremental

import "sort"

type NaiveModel struct {
	checker Checker
	state   naiveState
}

type naiveEdgeSet map[DeclID]map[DeclID]bool

type naiveState struct {
	declarations    map[DeclID]Declaration
	signatures      map[DeclID]Signature
	implementations map[DeclID]Signature
}

func NewNaiveModel(checker Checker) *NaiveModel {
	return &NaiveModel{
		checker: checker,
		state: naiveState{
			declarations:    map[DeclID]Declaration{},
			signatures:      map[DeclID]Signature{},
			implementations: map[DeclID]Signature{},
		},
	}
}

func (m *NaiveModel) Apply(edit Edit) error {
	current := m.state.declarations[edit.ID]
	switch edit.Type {
	case AddDeclaration:
		current.Present = true
		current.SignatureSource = edit.SignatureSource
		current.ImplementationSource = edit.ImplementationSource
	case SetSignature:
		current.SignatureSource = edit.SignatureSource
	case SetImplementation:
		current.ImplementationSource = edit.ImplementationSource
	case DeleteDeclaration:
		current.Present = false
	}
	current.ID = edit.ID
	m.state.declarations[edit.ID] = current
	m.rebuild()
	return nil
}

func (m *NaiveModel) signature(id DeclID) Signature {
	if signature, ok := m.state.signatures[id]; ok {
		return signature
	}
	return MissingSignature(id)
}

func (m *NaiveModel) Signature(id DeclID) Signature {
	return m.signature(id)
}

func (m *NaiveModel) Implementation(id DeclID) Signature {
	if signature, ok := m.state.implementations[id]; ok {
		return signature
	}
	return MissingSignature(id)
}

func (m *NaiveModel) rebuild() {
	ids := sortedNaiveIDs(m.state.declarations)
	signatures := map[DeclID]Signature{}
	edges := naiveEdgeSet{}

	for iteration := 0; iteration <= len(ids); iteration++ {
		next := map[DeclID]Signature{}
		reads := map[DeclID]map[DeclID]bool{}
		for _, id := range ids {
			decl := m.state.declarations[id]
			if !decl.Present {
				next[id] = MissingSignature(id)
				reads[id] = map[DeclID]bool{}
				continue
			}
			deps := map[DeclID]bool{}
			context := &CheckContext{
				id:    id,
				reads: deps,
				lookup: func(want DeclID) (SignatureResult, bool) {
					if signature, ok := signatures[want]; ok {
						return SignatureResult{Present: true, Signature: signature}, true
					}
					return SignatureResult{Present: false, Signature: MissingSignature(want)}, false
				},
			}
			next[id] = m.checker.CheckSignature(context, decl)
			reads[id] = deps
		}

		edges = naiveEdgeSet{}
		for owner, deps := range reads {
			edges[owner] = deps
		}
		for _, group := range naiveComponents(ids, edges) {
			failed := len(group) > 1
			for _, id := range group {
				if !m.state.declarations[id].Present {
					failed = true
				}
			}
			if failed {
				errorSignature := groupErrorSignature(group)
				for _, id := range group {
					if m.state.declarations[id].Present {
						next[id] = errorSignature
					}
				}
			}
		}

		if sameSignatures(signatures, next) {
			signatures = next
			break
		}
		signatures = next
	}

	m.state.signatures = signatures
	m.state.implementations = map[DeclID]Signature{}
	for _, id := range ids {
		decl := m.state.declarations[id]
		if !decl.Present {
			continue
		}
		context := &CheckContext{
			id:    id,
			reads: map[DeclID]bool{},
			lookup: func(want DeclID) (SignatureResult, bool) {
				if signature, ok := signatures[want]; ok {
					return SignatureResult{Present: m.state.declarations[want].Present, Signature: signature}, true
				}
				return SignatureResult{Present: false, Signature: MissingSignature(want)}, false
			},
		}
		m.state.implementations[id] = m.checker.CheckImplementation(context, decl)
	}
}

func sortedNaiveIDs(values map[DeclID]Declaration) []DeclID {
	ids := make([]DeclID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func naiveComponents(ids []DeclID, edges naiveEdgeSet) [][]DeclID {
	indices := map[DeclID]int{}
	low := map[DeclID]int{}
	onStack := map[DeclID]bool{}
	stack := []DeclID{}
	nextIndex := 0
	components := [][]DeclID{}

	var visit func(DeclID)
	visit = func(id DeclID) {
		indices[id] = nextIndex
		low[id] = nextIndex
		nextIndex++
		stack = append(stack, id)
		onStack[id] = true
		for dep := range edges[id] {
			if _, seen := indices[dep]; !seen && containsNaiveID(ids, dep) {
				visit(dep)
				if low[dep] < low[id] {
					low[id] = low[dep]
				}
			} else if onStack[dep] && indices[dep] < low[id] {
				low[id] = indices[dep]
			}
		}
		if low[id] == indices[id] {
			group := []DeclID{}
			for {
				node := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[node] = false
				group = append(group, node)
				if node == id {
					break
				}
			}
			sort.Slice(group, func(i, j int) bool { return group[i] < group[j] })
			components = append(components, group)
		}
	}

	for _, id := range ids {
		if _, ok := indices[id]; !ok {
			visit(id)
		}
	}
	return components
}

func containsNaiveID(ids []DeclID, target DeclID) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func sameSignatures(a, b map[DeclID]Signature) bool {
	if len(a) != len(b) {
		return false
	}
	for id, signature := range a {
		if !signature.Equal(b[id]) {
			return false
		}
	}
	return true
}
