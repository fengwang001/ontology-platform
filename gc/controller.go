package gc

import (
	"sort"
	"sync"
	"time"
)

// Controller is the public entry point of the cascade-deletion service.
//
// Concurrency: a single mutex serializes all operations and every mutating
// call converges the cascade before returning, so any concurrent execution
// is equivalent to some serial order and each cascade is observed
// atomically. The zero-value-ready constructor is New.
type Controller struct {
	mu     sync.RWMutex
	s      *store
	clock  func() time.Time
	obs    func(Event)
	stats  Stats
	hasObs bool
}

// Option customizes a Controller.
type Option func(*Controller)

// WithClock injects a time source. Operations performed in a single call
// share one clock reading, which keeps cascades reproducible in tests.
func WithClock(clock func() time.Time) Option {
	return func(c *Controller) { c.clock = clock }
}

// WithObserver registers a callback receiving every internal state
// transition, in the order it happens.
func WithObserver(obs func(Event)) Option {
	return func(c *Controller) {
		c.obs = obs
		c.hasObs = obs != nil
	}
}

// New creates an empty Controller.
func New(opts ...Option) *Controller {
	c := &Controller{s: newStore(), clock: time.Now}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// LastStats reports the amount of elementary work performed by the most
// recent mutating operation.
func (c *Controller) LastStats() Stats {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.stats
}

func (c *Controller) emit(ev Event) {
	if c.hasObs {
		c.obs(ev)
	}
}

// engineFor starts a convergence engine for one operation.
func (c *Controller) engineFor(now time.Time) *engine {
	c.stats = Stats{}
	return newEngine(c.s, now, c.emit, &c.stats)
}

// Create registers a new object. All owners must already exist and must not
// be deleting. A cycle cannot be created here: references always point to
// existing objects, so no existing object can reference the new ID.
func (c *Controller) Create(obj Object) error {
	if err := validateRefs(obj.ID, obj.Owners); err != nil {
		return err
	}
	for _, f := range obj.Finalizers {
		if f == "" {
			return errorf(KindInvalidArgument, "object %q: empty finalizer name", obj.ID)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.s.objs[obj.ID]; ok {
		return errorf(KindConflict, "object %q already exists", obj.ID)
	}
	for _, ref := range obj.Owners {
		owner, ok := c.s.objs[ref.ID]
		if !ok {
			return errorf(KindOwnerMissing, "object %q: owner %q does not exist", obj.ID, ref.ID)
		}
		if owner.Deleting {
			return errorf(KindOwnerMissing, "object %q: owner %q is being deleted", obj.ID, ref.ID)
		}
	}
	stored := &Object{
		ID:         obj.ID,
		Owners:     append([]OwnerReference(nil), obj.Owners...),
		Finalizers: dedupStrings(obj.Finalizers),
	}
	c.s.insert(stored)
	return nil
}

// Delete requests the deletion of id with the given policy. The call only
// marks the object and records policy and request time; the cascade engine
// then removes everything that has become removable before returning.
// Deleting an already deleting object is a successful no-op, except that a
// background deletion is upgraded to foreground.
func (c *Controller) Delete(id string, p Policy) error {
	if id == "" {
		return errorf(KindInvalidArgument, "empty object id")
	}
	if !p.Valid() {
		return errorf(KindInvalidArgument, "object %q: invalid deletion policy %d", id, int(p))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.s.objs[id]
	if !ok {
		return errorf(KindNotFound, "object %q does not exist", id)
	}
	eng := c.engineFor(c.clock())
	eng.markDeleting(o, p)
	eng.converge()
	return nil
}

// AddFinalizer appends a finalizer. Adding an existing name is a no-op.
// Adding to a deleting object is rejected.
func (c *Controller) AddFinalizer(id, finalizer string) error {
	if id == "" {
		return errorf(KindInvalidArgument, "empty object id")
	}
	if finalizer == "" {
		return errorf(KindInvalidArgument, "object %q: empty finalizer name", id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.s.objs[id]
	if !ok {
		return errorf(KindNotFound, "object %q does not exist", id)
	}
	if o.Deleting {
		return errorf(KindConflict, "object %q is being deleted", id)
	}
	for _, f := range o.Finalizers {
		if f == finalizer {
			return nil
		}
	}
	o.Finalizers = append(o.Finalizers, finalizer)
	return nil
}

// RemoveFinalizer deletes a finalizer. Removing the last one may unleash
// the whole pending cascade; all effects are applied before returning.
func (c *Controller) RemoveFinalizer(id, finalizer string) error {
	if id == "" {
		return errorf(KindInvalidArgument, "empty object id")
	}
	if finalizer == "" {
		return errorf(KindInvalidArgument, "object %q: empty finalizer name", id)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.s.objs[id]
	if !ok {
		return errorf(KindNotFound, "object %q does not exist", id)
	}
	idx := -1
	for i, f := range o.Finalizers {
		if f == finalizer {
			idx = i
			break
		}
	}
	if idx < 0 {
		return errorf(KindNotFound, "object %q: finalizer %q does not exist", id, finalizer)
	}
	o.Finalizers = append(o.Finalizers[:idx], o.Finalizers[idx+1:]...)
	eng := c.engineFor(c.clock())
	eng.remQ[id] = struct{}{}
	eng.converge()
	return nil
}

// SetOwners replaces the owner reference set of an object. The new set must
// reference existing, non-deleting objects, contain no duplicates or
// self-reference, and must not close a cycle. Deleting objects may not
// change owners.
func (c *Controller) SetOwners(id string, refs []OwnerReference) error {
	if err := validateRefs(id, refs); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.s.objs[id]
	if !ok {
		return errorf(KindNotFound, "object %q does not exist", id)
	}
	if o.Deleting {
		return errorf(KindConflict, "object %q is being deleted", id)
	}
	if c.s.wouldCycle(id, refs) {
		return errorf(KindCycle, "object %q: owner set would create a cycle", id)
	}
	for _, ref := range refs {
		owner, ok := c.s.objs[ref.ID]
		if !ok {
			return errorf(KindOwnerMissing, "object %q: owner %q does not exist", id, ref.ID)
		}
		if owner.Deleting {
			return errorf(KindOwnerMissing, "object %q: owner %q is being deleted", id, ref.ID)
		}
	}
	unblocked := c.s.replaceOwners(o, append([]OwnerReference(nil), refs...))
	if len(unblocked) > 0 {
		eng := c.engineFor(c.clock())
		for _, ownerID := range unblocked {
			eng.remQ[ownerID] = struct{}{}
		}
		eng.converge()
	}
	return nil
}

// Get returns a copy of the object, or false if it does not exist.
func (c *Controller) Get(id string) (Object, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	o, ok := c.s.objs[id]
	if !ok {
		return Object{}, false
	}
	return *o.clone(), true
}

// Snapshot returns copies of all objects, sorted by ID. It costs O(n) and
// is intended for tests, audits and debugging.
func (c *Controller) Snapshot() []Object {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Object, 0, len(c.s.objs))
	for _, o := range c.s.objs {
		out = append(out, *o.clone())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// validateRefs checks the argument-level rules for an owner reference set:
// non-empty object ID, non-empty owner IDs, no duplicates, no self
// reference. All are KindInvalidArgument violations.
func validateRefs(id string, refs []OwnerReference) error {
	if id == "" {
		return errorf(KindInvalidArgument, "empty object id")
	}
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if ref.ID == "" {
			return errorf(KindInvalidArgument, "object %q: empty owner id", id)
		}
		if ref.ID == id {
			return errorf(KindInvalidArgument, "object %q: self owner reference", id)
		}
		if _, dup := seen[ref.ID]; dup {
			return errorf(KindInvalidArgument, "object %q: duplicate owner %q", id, ref.ID)
		}
		seen[ref.ID] = struct{}{}
	}
	return nil
}

func dedupStrings(in []string) []string {
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
