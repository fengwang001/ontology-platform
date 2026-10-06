package cascade

import "strings"

func validatePolicy(policy Policy) error {
	switch policy {
	case Background, Foreground, Orphan:
		return nil
	default:
		return newError(InvalidArgument, "invalid deletion policy")
	}
}

func validateRequiredID(id, name string) error {
	if strings.TrimSpace(id) == "" {
		return newError(InvalidArgument, name+" must be non-empty")
	}
	return nil
}

func validateOwnerRefs(owners []OwnerRef) (map[string]bool, error) {
	result := make(map[string]bool, len(owners))
	for _, owner := range owners {
		if strings.TrimSpace(owner.OwnerID) == "" {
			return nil, newError(InvalidArgument, "owner id must be non-empty")
		}
		if _, exists := result[owner.OwnerID]; exists {
			return nil, newError(InvalidArgument, "duplicate owner reference: "+owner.OwnerID)
		}
		result[owner.OwnerID] = owner.BlockDeletion
	}
	return result, nil
}

func validateFinalizers(finalizers []string) (map[string]struct{}, error) {
	result := make(map[string]struct{}, len(finalizers))
	for _, finalizer := range finalizers {
		if strings.TrimSpace(finalizer) == "" {
			return nil, newError(InvalidArgument, "finalizer must be non-empty")
		}
		if _, exists := result[finalizer]; exists {
			return nil, newError(InvalidArgument, "duplicate finalizer: "+finalizer)
		}
		result[finalizer] = struct{}{}
	}
	return result, nil
}

func (c *Controller) validateCreate(input CreateObjectInput) (map[string]bool, map[string]struct{}, error) {
	if err := validateRequiredID(input.ID, "object id"); err != nil {
		return nil, nil, err
	}
	owners, err := validateOwnerRefs(input.Owners)
	if err != nil {
		return nil, nil, err
	}
	finalizers, err := validateFinalizers(input.Finalizers)
	if err != nil {
		return nil, nil, err
	}
	if c.graph.get(input.ID) != nil {
		return nil, nil, newError(InvalidArgument, "object already exists: "+input.ID)
	}
	if err := c.validateOwnerTargets(input.ID, owners); err != nil {
		return nil, nil, err
	}
	return owners, finalizers, nil
}

func (c *Controller) validateOwnerTargets(selfID string, owners map[string]bool) error {
	if _, selfOwner := owners[selfID]; selfOwner {
		return newError(CycleDetected, "object must not reference itself")
	}
	if c.hasCycleFrom(selfID, owners) {
		return newError(CycleDetected, "owner relation would form a cycle")
	}
	for ownerID := range owners {
		owner := c.graph.get(ownerID)
		if owner == nil {
			return newError(OwnerMissing, "owner does not exist: "+ownerID)
		}
		if owner.deleting {
			return newError(OwnerMissing, "owner is deleting: "+ownerID)
		}
	}
	return nil
}

func (c *Controller) hasCycleFrom(selfID string, newOwners map[string]bool) bool {
	visited := make(map[string]bool)
	stack := make([]string, 0, len(newOwners))
	for ownerID := range newOwners {
		stack = append(stack, ownerID)
	}
	for len(stack) > 0 {
		currentID := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[currentID] {
			continue
		}
		visited[currentID] = true
		current := c.graph.get(currentID)
		if current == nil {
			continue
		}
		if currentID == selfID {
			return true
		}
		for ownerID := range current.owners {
			if ownerID == selfID {
				return true
			}
			if !visited[ownerID] {
				stack = append(stack, ownerID)
			}
		}
	}
	return false
}
