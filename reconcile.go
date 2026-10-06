package cascade

import "sort"

type reconciler struct {
	graph        *objectGraph
	objectsVisit map[string]struct{}
	edgesUsed    int
}

func newReconciler(graph *objectGraph) *reconciler {
	return &reconciler{
		graph:        graph,
		objectsVisit: make(map[string]struct{}),
	}
}

func (r *reconciler) enqueueSeed(queue *[]string, queued map[string]struct{}, node *objectNode, markChanged bool) {
	r.visit(node.id)
	if markChanged {
		r.enqueueCoOwners(queue, queued, node)
	}
	*queue = append(*queue, node.id)
	queued[node.id] = struct{}{}
}

func (r *reconciler) converge(ids []string, changedIDs []string, at int64) ([]string, Stats) {
	changed := make(map[string]struct{}, len(changedIDs))
	for _, id := range changedIDs {
		changed[id] = struct{}{}
	}
	queue := make([]string, 0, len(ids))
	queued := make(map[string]struct{})
	for _, id := range ids {
		if node := r.graph.get(id); node != nil {
			_, isChanged := changed[id]
			r.enqueueSeed(&queue, queued, node, isChanged)
		}
	}

	removed := make([]string, 0)
	for len(queue) > 0 {
		sort.Strings(queue)
		id := queue[0]
		queue = queue[1:]
		delete(queued, id)

		node := r.graph.get(id)
		if node == nil {
			continue
		}
		r.visit(id)

		if node.deleting && node.policy == Foreground {
			for _, dependentID := range r.sortedDependents(node) {
				r.useEdge()
				dependent := r.graph.get(dependentID)
				if dependent == nil {
					continue
				}
				r.propagateForegroundTo(&queue, queued, dependent, node, at)
			}
		}

		if !r.removable(node) {
			continue
		}

		dependentIDs := r.sortedDependents(node)
		ownerIDs := r.sortedOwners(node)
		parentPolicy := node.policy
		for _, dependentID := range dependentIDs {
			r.useEdge()
			r.visit(dependentID)
			r.graph.unlinkOwner(dependentID, node.id)
		}
		for _, ownerID := range ownerIDs {
			r.useEdge()
			r.visit(ownerID)
			r.graph.unlinkOwner(node.id, ownerID)
		}
		r.graph.removeObject(node.id)
		removed = append(removed, node.id)

		for _, dependentID := range dependentIDs {
			dependent := r.graph.get(dependentID)
			if dependent == nil {
				continue
			}
			if parentPolicy != Orphan && len(dependent.owners) == 0 {
				if !dependent.deleting {
					dependent.deleting = true
					dependent.policy = Background
					dependent.deleteAt = at
					r.enqueueCoOwners(&queue, queued, dependent)
				}
				r.enqueue(&queue, queued, dependentID)
			}
		}

		for _, dependentID := range dependentIDs {
			r.enqueue(&queue, queued, dependentID)
		}

		for _, ownerID := range ownerIDs {
			r.enqueue(&queue, queued, ownerID)
		}
		for _, dependentID := range dependentIDs {
			dependent := r.graph.get(dependentID)
			if dependent == nil {
				continue
			}
			for remainingOwnerID := range dependent.owners {
				r.useEdge()
				owner := r.graph.get(remainingOwnerID)
				if owner != nil && owner.deleting && owner.policy == Foreground {
					r.propagateForegroundTo(&queue, queued, dependent, owner, at)
				}
			}
		}
	}
	return removed, Stats{ObjectsVisited: len(r.objectsVisit), ReferencesUsed: r.edgesUsed}
}

func (r *reconciler) visit(id string) {
	r.objectsVisit[id] = struct{}{}
}

func (r *reconciler) useEdge() {
	r.edgesUsed++
}

func (r *reconciler) enqueue(queue *[]string, queued map[string]struct{}, id string) {
	if node := r.graph.get(id); node != nil {
		r.visit(id)
		if _, exists := queued[id]; !exists {
			*queue = append(*queue, id)
			queued[id] = struct{}{}
		}
	}
}

func (r *reconciler) enqueueCoOwners(queue *[]string, queued map[string]struct{}, node *objectNode) {
	for _, dependentID := range r.sortedDependents(node) {
		r.useEdge()
		dependent := r.graph.get(dependentID)
		if dependent == nil {
			continue
		}
		for ownerID := range dependent.owners {
			if ownerID != node.id {
				r.useEdge()
				r.enqueue(queue, queued, ownerID)
			}
		}
	}
}

func (r *reconciler) propagateForegroundTo(queue *[]string, queued map[string]struct{}, dependent, owner *objectNode, at int64) {
	if !owner.deleting || owner.policy != Foreground || dependent == nil {
		return
	}
	if !r.allOtherOwnersDeleting(dependent, owner.id) {
		return
	}
	if !dependent.deleting {
		dependent.deleting = true
		dependent.deleteAt = at
		r.enqueueCoOwners(queue, queued, dependent)
	}
	dependent.policy = Foreground
	r.enqueue(queue, queued, dependent.id)
}

func (r *reconciler) removable(node *objectNode) bool {
	r.visit(node.id)
	return node.deleting && len(node.finalizers) == 0 &&
		(node.policy != Foreground || node.blockingDependents == 0)
}

func (r *reconciler) sortedDependents(node *objectNode) []string {
	result := sortedKeys(node.dependents)
	sort.Strings(result)
	return result
}

func (r *reconciler) sortedOwners(node *objectNode) []string {
	result := sortedKeys(node.owners)
	sort.Strings(result)
	return result
}

func sortedKeys[V any](m map[string]V) []string {
	result := make([]string, 0, len(m))
	for key := range m {
		result = append(result, key)
	}
	return result
}

func (r *reconciler) allOtherOwnersDeleting(node *objectNode, foregroundOwner string) bool {
	ownerIDs := make([]string, 0, len(node.owners))
	for ownerID := range node.owners {
		if ownerID != foregroundOwner {
			ownerIDs = append(ownerIDs, ownerID)
		}
	}
	r.edgesUsed += len(ownerIDs)
	for _, ownerID := range ownerIDs {
		r.visit(ownerID)
		owner := r.graph.get(ownerID)
		if owner != nil && !owner.deleting {
			return false
		}
	}
	return true
}
