package cascade

import "sync"

type Controller struct {
	mu    sync.Mutex
	graph *objectGraph
}

// NewController creates an empty cascade-delete controller.
func NewController() *Controller {
	return &Controller{graph: newObjectGraph()}
}

// Create validates and inserts an object with owners and finalizers.
func (c *Controller) Create(input CreateObjectInput) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	owners, finalizers, err := c.validateCreate(input)
	if err != nil {
		return err
	}

	c.graph.addObject(CreateObjectInput{ID: input.ID})
	node := c.graph.get(input.ID)
	for ownerID, blocking := range owners {
		c.graph.linkOwner(input.ID, ownerID, blocking)
	}
	node.finalizers = finalizers
	return nil
}

// Delete marks an object deleting, upgrades background to foreground, and converges immediately.
func (c *Controller) Delete(id string, policy Policy, at int64) (OperationResult, error) {
	if err := validateRequiredID(id, "object id"); err != nil {
		return OperationResult{}, err
	}
	if err := validatePolicy(policy); err != nil {
		return OperationResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	node := c.graph.get(id)
	if node == nil {
		return OperationResult{}, newError(ObjectNotFound, "object does not exist: "+id)
	}

	if node.deleting {
		result := OperationResult{ObjectID: id, NoChange: true}
		if node.policy == Background && policy == Foreground {
			node.policy = Foreground
			result.NoChange = false
			result.Upgraded = true
			result.Removed, result.Stats = newReconciler(c.graph).converge([]string{id}, []string{id}, at)
		}
		return result, nil
	}

	node.deleting = true
	node.policy = policy
	node.deleteAt = at
	result := OperationResult{ObjectID: id}
	result.Removed, result.Stats = newReconciler(c.graph).converge([]string{id}, []string{id}, at)
	return result, nil
}

// AddFinalizer appends a finalizer to a non-deleting object.
func (c *Controller) AddFinalizer(id, finalizer string) error {
	if err := validateRequiredID(id, "object id"); err != nil {
		return err
	}
	if err := validateRequiredID(finalizer, "finalizer"); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	node := c.graph.get(id)
	if node == nil {
		return newError(ObjectNotFound, "object does not exist: "+id)
	}
	if node.deleting {
		return newError(Conflict, "cannot add finalizer to deleting object: "+id)
	}
	node.finalizers[finalizer] = struct{}{}
	return nil
}

// RemoveFinalizer removes a finalizer and converges any unblocked cascade.
func (c *Controller) RemoveFinalizer(id, finalizer string, at int64) (OperationResult, error) {
	if err := validateRequiredID(id, "object id"); err != nil {
		return OperationResult{}, err
	}
	if err := validateRequiredID(finalizer, "finalizer"); err != nil {
		return OperationResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	node := c.graph.get(id)
	if node == nil {
		return OperationResult{}, newError(ObjectNotFound, "object does not exist: "+id)
	}
	if _, exists := node.finalizers[finalizer]; !exists {
		return OperationResult{}, newError(InvalidArgument, "finalizer does not exist: "+finalizer)
	}

	delete(node.finalizers, finalizer)
	result := OperationResult{ObjectID: id}
	result.Removed, result.Stats = newReconciler(c.graph).converge([]string{id}, nil, at)
	return result, nil
}

// ReplaceOwners atomically validates and replaces an object's owner references.
func (c *Controller) ReplaceOwners(id string, owners []OwnerRef, at int64) (OperationResult, error) {
	if err := validateRequiredID(id, "object id"); err != nil {
		return OperationResult{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	node := c.graph.get(id)
	if node == nil {
		return OperationResult{}, newError(ObjectNotFound, "object does not exist: "+id)
	}
	if node.deleting {
		return OperationResult{}, newError(Conflict, "cannot replace owners of deleting object: "+id)
	}

	validOwners, err := validateOwnerRefs(owners)
	if err != nil {
		return OperationResult{}, err
	}
	if err := c.validateOwnerTargets(id, validOwners); err != nil {
		return OperationResult{}, err
	}

	removedOwnerIDs := make([]string, 0)
	for _, oldOwner := range c.graph.stateOf(node).Owners {
		if _, exists := validOwners[oldOwner.OwnerID]; !exists {
			removedOwnerIDs = append(removedOwnerIDs, oldOwner.OwnerID)
			c.graph.unlinkOwner(id, oldOwner.OwnerID)
		}
	}
	for ownerID, blocking := range validOwners {
		oldEdge, exists := node.owners[ownerID]
		switch {
		case !exists:
			c.graph.linkOwner(id, ownerID, blocking)
		case oldEdge.blocking != blocking:
			c.graph.unlinkOwner(id, ownerID)
			c.graph.linkOwner(id, ownerID, blocking)
		}
	}

	result := OperationResult{ObjectID: id}
	if len(removedOwnerIDs) > 0 {
		seeds := append(removedOwnerIDs, id)
		result.Removed, result.Stats = newReconciler(c.graph).converge(seeds, removedOwnerIDs, at)
	}
	return result, nil
}

// Snapshot returns canonical states of all live objects.
func (c *Controller) Snapshot() map[string]ObjectState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.graph.snapshot()
}
