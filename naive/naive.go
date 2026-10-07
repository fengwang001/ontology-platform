// Package naive is an independent, deliberately simple reference
// implementation of the same cascade-deletion semantics as package gc.
// It keeps no indexes: every rule is evaluated by scanning all objects,
// and convergence is a brute-force fixpoint computation. It exists to be
// diff-tested against the optimized controller on random object graphs.
package naive

import (
	"sort"
	"time"

	"ontology/gc"
)

// Controller mirrors gc.Controller with the same public surface.
type Controller struct {
	objs  map[string]*gc.Object
	clock func() time.Time
	obs   func(gc.Event)
}

// New creates an empty model. clock and obs may be nil.
func New(clock func() time.Time, obs func(gc.Event)) *Controller {
	if clock == nil {
		clock = time.Now
	}
	return &Controller{objs: make(map[string]*gc.Object), clock: clock, obs: obs}
}

func (c *Controller) emit(ev gc.Event) {
	if c.obs != nil {
		c.obs(ev)
	}
}

func (c *Controller) sortedIDs() []string {
	ids := make([]string, 0, len(c.objs))
	for id := range c.objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Create mirrors gc.Controller.Create.
func (c *Controller) Create(obj gc.Object) error {
	if err := checkRefs(obj.ID, obj.Owners); err != nil {
		return err
	}
	for _, f := range obj.Finalizers {
		if f == "" {
			return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty finalizer name"}
		}
	}
	if _, ok := c.objs[obj.ID]; ok {
		return &gc.Error{Kind: gc.KindConflict, Message: "object already exists"}
	}
	for _, ref := range obj.Owners {
		owner, ok := c.objs[ref.ID]
		if !ok || owner.Deleting {
			return &gc.Error{Kind: gc.KindOwnerMissing, Message: "owner does not exist or is deleting"}
		}
	}
	stored := &gc.Object{
		ID:         obj.ID,
		Owners:     append([]gc.OwnerReference(nil), obj.Owners...),
		Finalizers: dedup(obj.Finalizers),
	}
	c.objs[stored.ID] = stored
	return nil
}

// Delete mirrors gc.Controller.Delete.
func (c *Controller) Delete(id string, p gc.Policy) error {
	if id == "" || !p.Valid() {
		return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty id or invalid policy"}
	}
	o, ok := c.objs[id]
	if !ok {
		return &gc.Error{Kind: gc.KindNotFound, Message: "object does not exist"}
	}
	c.markDeleting(o, p, c.clock())
	c.converge()
	return nil
}

// AddFinalizer mirrors gc.Controller.AddFinalizer.
func (c *Controller) AddFinalizer(id, finalizer string) error {
	if id == "" || finalizer == "" {
		return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty id or finalizer"}
	}
	o, ok := c.objs[id]
	if !ok {
		return &gc.Error{Kind: gc.KindNotFound, Message: "object does not exist"}
	}
	if o.Deleting {
		return &gc.Error{Kind: gc.KindConflict, Message: "object is being deleted"}
	}
	for _, f := range o.Finalizers {
		if f == finalizer {
			return nil
		}
	}
	o.Finalizers = append(o.Finalizers, finalizer)
	return nil
}

// RemoveFinalizer mirrors gc.Controller.RemoveFinalizer.
func (c *Controller) RemoveFinalizer(id, finalizer string) error {
	if id == "" || finalizer == "" {
		return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty id or finalizer"}
	}
	o, ok := c.objs[id]
	if !ok {
		return &gc.Error{Kind: gc.KindNotFound, Message: "object does not exist"}
	}
	idx := -1
	for i, f := range o.Finalizers {
		if f == finalizer {
			idx = i
			break
		}
	}
	if idx < 0 {
		return &gc.Error{Kind: gc.KindNotFound, Message: "finalizer does not exist"}
	}
	o.Finalizers = append(o.Finalizers[:idx], o.Finalizers[idx+1:]...)
	c.converge()
	return nil
}

// SetOwners mirrors gc.Controller.SetOwners.
func (c *Controller) SetOwners(id string, refs []gc.OwnerReference) error {
	if err := checkRefs(id, refs); err != nil {
		return err
	}
	o, ok := c.objs[id]
	if !ok {
		return &gc.Error{Kind: gc.KindNotFound, Message: "object does not exist"}
	}
	if o.Deleting {
		return &gc.Error{Kind: gc.KindConflict, Message: "object is being deleted"}
	}
	if c.wouldCycle(id, refs) {
		return &gc.Error{Kind: gc.KindCycle, Message: "owner set would create a cycle"}
	}
	for _, ref := range refs {
		owner, ok := c.objs[ref.ID]
		if !ok || owner.Deleting {
			return &gc.Error{Kind: gc.KindOwnerMissing, Message: "owner does not exist or is deleting"}
		}
	}
	o.Owners = append([]gc.OwnerReference(nil), refs...)
	c.converge() // removing a blocking reference may unblock a foreground owner
	return nil
}

// Get mirrors gc.Controller.Get.
func (c *Controller) Get(id string) (gc.Object, bool) {
	o, ok := c.objs[id]
	if !ok {
		return gc.Object{}, false
	}
	return *o, true
}

// Snapshot mirrors gc.Controller.Snapshot.
func (c *Controller) Snapshot() []gc.Object {
	out := make([]gc.Object, 0, len(c.objs))
	for _, id := range c.sortedIDs() {
		out = append(out, *c.objs[id])
	}
	return out
}

// --- convergence: brute-force fixpoint in the canonical order ---

func (c *Controller) converge() {
	for {
		changed := false
		// Phase 1: foreground propagation to a fixpoint.
		for {
			progressed := false
			for _, id := range c.sortedIDs() {
				o := c.objs[id]
				if o == nil || (o.Deleting && o.Policy != gc.PolicyBackground) {
					continue
				}
				if c.propagationEligible(o) {
					c.markDeleting(o, gc.PolicyForeground, c.clock())
					progressed = true
					changed = true
				}
			}
			if !progressed {
				break
			}
		}
		// Phase 2: one removal pass.
		for _, id := range c.sortedIDs() {
			o := c.objs[id]
			if o == nil || !c.removable(o) {
				continue
			}
			c.remove(o)
			changed = true
		}
		if !changed {
			return
		}
	}
}

func (c *Controller) markDeleting(o *gc.Object, p gc.Policy, now time.Time) {
	if o.Deleting {
		if o.Policy == gc.PolicyBackground && p == gc.PolicyForeground {
			o.Policy = gc.PolicyForeground
			c.emit(gc.Event{Kind: gc.EventUpgradeForeground, ID: o.ID, Policy: p})
		}
		return
	}
	o.Deleting = true
	o.Policy = p
	o.DeletionRequestedAt = now
	c.emit(gc.Event{Kind: gc.EventMarkDeleting, ID: o.ID, Policy: p})
}

// propagationEligible reports whether o must join a foreground deletion:
// at least one owner is deleting in foreground and every owner is gone or
// deleting.
func (c *Controller) propagationEligible(o *gc.Object) bool {
	if len(o.Owners) == 0 {
		return false
	}
	hasForeground := false
	for _, ref := range o.Owners {
		owner, ok := c.objs[ref.ID]
		if !ok {
			continue
		}
		if !owner.Deleting {
			return false
		}
		if owner.Policy == gc.PolicyForeground {
			hasForeground = true
		}
	}
	return hasForeground
}

// removable reports whether every removal condition holds for o.
func (c *Controller) removable(o *gc.Object) bool {
	if !o.Deleting || len(o.Finalizers) > 0 {
		return false
	}
	if o.Policy != gc.PolicyForeground {
		return true
	}
	// Foreground: no live blocking dependent may remain.
	for _, other := range c.objs {
		if other.Deleting {
			continue
		}
		for _, ref := range other.Owners {
			if ref.ID == o.ID && ref.Block {
				return false
			}
		}
	}
	return true
}

func (c *Controller) remove(o *gc.Object) {
	c.emit(gc.Event{Kind: gc.EventRemove, ID: o.ID})
	now := c.clock()
	for _, id := range c.sortedIDs() {
		dep := c.objs[id]
		detached := false
		kept := dep.Owners[:0]
		for _, ref := range dep.Owners {
			if ref.ID == o.ID {
				detached = true
				continue
			}
			kept = append(kept, ref)
		}
		if !detached {
			continue
		}
		dep.Owners = kept
		c.emit(gc.Event{Kind: gc.EventDetach, ID: dep.ID, Owner: o.ID})
		if !dep.Deleting && o.Policy != gc.PolicyOrphan && len(dep.Owners) == 0 {
			c.markDeleting(dep, gc.PolicyBackground, now)
		}
	}
	delete(c.objs, o.ID)
}

// wouldCycle walks the owner graph upwards from id (using the proposed
// references for id) and reports whether any proposed owner is reachable.
func (c *Controller) wouldCycle(id string, refs []gc.OwnerReference) bool {
	targets := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		targets[ref.ID] = struct{}{}
	}
	visited := map[string]struct{}{id: {}}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		var curRefs []gc.OwnerReference
		if cur == id {
			curRefs = refs
		} else if o, ok := c.objs[cur]; ok {
			curRefs = o.Owners
		}
		for _, ref := range curRefs {
			if _, ok := targets[ref.ID]; ok {
				return true
			}
			if _, ok := visited[ref.ID]; !ok {
				visited[ref.ID] = struct{}{}
				stack = append(stack, ref.ID)
			}
		}
	}
	return false
}

func checkRefs(id string, refs []gc.OwnerReference) error {
	if id == "" {
		return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty object id"}
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			return &gc.Error{Kind: gc.KindInvalidArgument, Message: "empty owner id"}
		}
		if ref.ID == id {
			return &gc.Error{Kind: gc.KindInvalidArgument, Message: "self owner reference"}
		}
		if _, dup := seen[ref.ID]; dup {
			return &gc.Error{Kind: gc.KindInvalidArgument, Message: "duplicate owner"}
		}
		seen[ref.ID] = struct{}{}
	}
	return nil
}

func dedup(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
