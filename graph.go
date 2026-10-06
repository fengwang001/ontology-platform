package cascade

import "sort"

type edge struct {
	blocking bool
}

type objectGraph struct {
	objects map[string]*objectNode
}

type objectNode struct {
	id                 string
	owners             map[string]edge
	dependents         map[string]edge
	finalizers         map[string]struct{}
	deleting           bool
	policy             Policy
	deleteAt           int64
	blockingDependents int
}

func newObjectGraph() *objectGraph {
	return &objectGraph{objects: make(map[string]*objectNode)}
}

func (g *objectGraph) get(id string) *objectNode {
	return g.objects[id]
}

func (g *objectGraph) addObject(input CreateObjectInput) {
	node := &objectNode{
		id:         input.ID,
		owners:     make(map[string]edge),
		dependents: make(map[string]edge),
		finalizers: make(map[string]struct{}),
	}
	for _, owner := range input.Owners {
		node.owners[owner.OwnerID] = edge{blocking: owner.BlockDeletion}
	}
	for _, finalizer := range input.Finalizers {
		node.finalizers[finalizer] = struct{}{}
	}
	g.objects[node.id] = node
}

func (g *objectGraph) linkOwner(dependent, owner string, blocking bool) {
	dependentNode := g.objects[dependent]
	ownerNode := g.objects[owner]
	dependentNode.owners[owner] = edge{blocking: blocking}
	ownerNode.dependents[dependent] = edge{blocking: blocking}
	if blocking {
		ownerNode.blockingDependents++
	}
}

func (g *objectGraph) unlinkOwner(dependent, owner string) bool {
	dependentNode := g.objects[dependent]
	ownerNode := g.objects[owner]
	ownerEdge := dependentNode.owners[owner]
	dependentEdge := ownerNode.dependents[dependent]
	blocking := ownerEdge.blocking || dependentEdge.blocking
	delete(dependentNode.owners, owner)
	delete(ownerNode.dependents, dependent)
	if blocking && ownerNode.blockingDependents > 0 {
		ownerNode.blockingDependents--
	}
	return blocking
}

func (g *objectGraph) removeObject(id string) {
	delete(g.objects, id)
}

func (g *objectGraph) snapshot() map[string]ObjectState {
	result := make(map[string]ObjectState, len(g.objects))
	for id, node := range g.objects {
		result[id] = g.stateOf(node)
	}
	return result
}

func (g *objectGraph) stateOf(node *objectNode) ObjectState {
	owners := make([]OwnerRef, 0, len(node.owners))
	for ownerID, ownerEdge := range node.owners {
		owners = append(owners, OwnerRef{OwnerID: ownerID, BlockDeletion: ownerEdge.blocking})
	}
	sortOwnerRefs(owners)

	finalizers := make([]string, 0, len(node.finalizers))
	for finalizer := range node.finalizers {
		finalizers = append(finalizers, finalizer)
	}
	sortStrings(finalizers)

	return ObjectState{
		ID:         node.id,
		Owners:     owners,
		Finalizers: finalizers,
		Deleting:   node.deleting,
		Policy:     node.policy,
		DeleteAt:   node.deleteAt,
	}
}

func sortOwnerRefs(owners []OwnerRef) {
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].OwnerID == owners[j].OwnerID {
			return owners[i].BlockDeletion && !owners[j].BlockDeletion
		}
		return owners[i].OwnerID < owners[j].OwnerID
	})
}

func sortStrings(values []string) {
	sort.Strings(values)
}
