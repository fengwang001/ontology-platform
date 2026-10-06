package cg

import (
	"fmt"
	"sort"
	"sync"
)

// Coordinator is the multi-volume consistency-group snapshot coordinator.
// All operations are serialized by an internal mutex; concurrent calls are
// equivalent to some serial order, and replaying the same operation
// sequence yields identical snapshot records and write sequence numbers.
type Coordinator struct {
	mu       sync.Mutex
	cfg      Config
	lastTime int64
	groups   map[string]*group
	volumes  map[string]*volume
	nextSnap uint64
	tracer   Tracer
}

// New constructs a Coordinator with the given configuration.
func New(cfg Config) *Coordinator {
	if cfg.DefaultQueueCapacity < 0 || cfg.MaxFreezeHold < 0 {
		panic("cg: invalid coordinator config")
	}
	return &Coordinator{
		cfg:      cfg,
		groups:   map[string]*group{},
		volumes:  map[string]*volume{},
		tracer:   nopTracer{},
		nextSnap: 1,
	}
}

// run centralizes the mandated rejection ordering:
//  1. parameter validation,
//  2. clock regression check,
//  3. timeout auto-abort (a consequence of time advancing; it is a real
//     state change even when the triggering operation is later rejected),
//  4. the operation's own validation and mutation.
func (c *Coordinator) run(name, input string, t int64, validate func() *Error, timeoutGroup string, act func(ctx *opCtx)) *Error {
	c.mu.Lock()
	defer c.mu.Unlock()

	d := Decision{At: t, Op: name, Input: input}
	if validate != nil {
		if e := validate(); e != nil {
			d.Outcome = "rejected"
			d.Reason = e.Kind.String()
			d.Detail = e.Message
			c.trace(d)
			return e
		}
	}
	if t < c.lastTime {
		d.Outcome = "rejected"
		d.Reason = ClockBackward.String()
		d.Detail = fmt.Sprintf("t=%d < last=%d", t, c.lastTime)
		c.trace(d)
		return errf(ClockBackward, "t=%d < last=%d", t, c.lastTime)
	}
	// Timestamp is accepted; monotonic time advances even if the operation
	// is subsequently rejected for an operational reason.
	c.lastTime = t

	ctx := &opCtx{c: c, d: d, at: t}
	if timeoutGroup != "" {
		if grp, ok := c.groups[timeoutGroup]; ok {
			ctx.timeout(grp)
		}
	}
	act(ctx)
	c.trace(ctx.d)
	return nil
}

type opCtx struct {
	c  *Coordinator
	d  Decision
	at int64
}

// timeout performs both timeout checks for grp at the current operation
// time and auto-aborts exactly as a manual abort would. It runs before any
// operation-specific judgement.
func (x *opCtx) timeout(grp *group) {
	if grp == nil || grp.snap == nil {
		return
	}
	if due, why := grp.snap.dueAutoAbort(x.at, grp.maxHold); due {
		grp.abort()
		x.note("auto-abort: " + why)
	}
}

func (x *opCtx) note(s string) {
	if x.d.Detail == "" {
		x.d.Detail = s
	} else {
		x.d.Detail += "; " + s
	}
}

func (x *opCtx) ok(detail string) {
	x.d.Outcome = "accepted"
	x.d.Reason = "ok"
	x.d.Detail = detail
}

func (x *opCtx) reject(e *Error) *Error {
	x.d.Outcome = "rejected"
	x.d.Reason = e.Kind.String()
	x.note(e.Message)
	return e
}

func nonEmpty(s string) bool { return s != "" }

func (c *Coordinator) CreateGroup(t int64, spec GroupSpec) error {
	var e *Error
	e0 := c.run("create_group", fmt.Sprintf("group=%q volumes=%v", spec.GroupID, spec.VolumeIDs), t,
		func() *Error {
			if !nonEmpty(spec.GroupID) || len(spec.VolumeIDs) < 2 || len(spec.VolumeIDs) > 16 {
				return errf(InvalidArgument, "group id non-empty and 2..16 volumes required")
			}
			seen := make(map[string]bool, len(spec.VolumeIDs))
			for _, vid := range spec.VolumeIDs {
				if !nonEmpty(vid) || seen[vid] {
					return errf(InvalidArgument, "duplicate or empty volume id: %q", vid)
				}
				seen[vid] = true
			}
			for vid, capv := range spec.Capacities {
				if !nonEmpty(vid) || capv < 0 {
					return errf(InvalidArgument, "bad capacity for %q", vid)
				}
			}
			return nil
		},
		"",
		func(ctx *opCtx) {
			if _, exists := c.groups[spec.GroupID]; exists {
				e = ctx.reject(errf(Conflict, "group %q already exists", spec.GroupID))
				return
			}
			for _, vid := range spec.VolumeIDs {
				if _, taken := c.volumes[vid]; taken {
					e = ctx.reject(errf(Conflict, "volume %q already belongs to another group", vid))
					return
				}
			}
			members := make([]*volume, 0, len(spec.VolumeIDs))
			for _, vid := range spec.VolumeIDs {
				capv := c.cfg.DefaultQueueCapacity
				if cv, ok := spec.Capacities[vid]; ok {
					capv = cv
				}
				members = append(members, newVolume(vid, capv))
			}
			grp := newGroup(spec.GroupID, members, c.cfg.MaxFreezeHold)
			c.groups[grp.id] = grp
			for _, v := range members {
				v.group = grp
				c.volumes[v.id] = v
			}
			ctx.ok("group created")
		})
	if e0 != nil {
		return e0
	}
	if e != nil {
		return e
	}
	return nil
}

func (c *Coordinator) AddMembers(t int64, groupID string, volumeIDs []string) error {
	var e *Error
	e0 := c.run("add_members", fmt.Sprintf("group=%q volumes=%v", groupID, volumeIDs), t,
		func() *Error {
			if !nonEmpty(groupID) || len(volumeIDs) == 0 {
				return errf(InvalidArgument, "group id and at least one volume required")
			}
			seen := map[string]bool{}
			for _, vid := range volumeIDs {
				if !nonEmpty(vid) || seen[vid] {
					return errf(InvalidArgument, "duplicate or empty volume id: %q", vid)
				}
				seen[vid] = true
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if !grp.idle() {
				e = ctx.reject(errf(StateError, "snapshot in progress"))
				return
			}
			if len(grp.members)+len(volumeIDs) > 16 {
				e = ctx.reject(errf(InvalidArgument, "group size would exceed 16"))
				return
			}
			for _, vid := range volumeIDs {
				if grp.has(vid) {
					e = ctx.reject(errf(InvalidArgument, "volume %q already in this group", vid))
					return
				}
				if _, taken := c.volumes[vid]; taken {
					e = ctx.reject(errf(Conflict, "volume %q already belongs to another group", vid))
					return
				}
			}
			for _, vid := range volumeIDs {
				v := newVolume(vid, c.cfg.DefaultQueueCapacity)
				v.group = grp
				c.volumes[vid] = v
				grp.members = append(grp.members, v)
				grp.index[v.id] = v
			}
			ctx.ok(fmt.Sprintf("%d members added", len(volumeIDs)))
		})
	if e0 != nil {
		return e0
	}
	if e != nil {
		return e
	}
	return nil
}

func (c *Coordinator) RemoveMembers(t int64, groupID string, volumeIDs []string) error {
	var e *Error
	e0 := c.run("remove_members", fmt.Sprintf("group=%q volumes=%v", groupID, volumeIDs), t,
		func() *Error {
			if !nonEmpty(groupID) || len(volumeIDs) == 0 {
				return errf(InvalidArgument, "group id and at least one volume required")
			}
			seen := map[string]bool{}
			for _, vid := range volumeIDs {
				if !nonEmpty(vid) || seen[vid] {
					return errf(InvalidArgument, "duplicate or empty volume id: %q", vid)
				}
				seen[vid] = true
			}
			return nil
		},
		groupID,
		func(ctx *opCtx) {
			grp, ok := c.groups[groupID]
			if !ok {
				e = ctx.reject(errf(NotFound, "group %q not found", groupID))
				return
			}
			ctx.timeout(grp)
			if !grp.idle() {
				e = ctx.reject(errf(StateError, "snapshot in progress"))
				return
			}
			for _, vid := range volumeIDs {
				if !grp.has(vid) {
					e = ctx.reject(errf(NotFound, "volume %q not in group %q", vid, groupID))
					return
				}
			}
			remaining := len(grp.members) - len(volumeIDs)
			if remaining < 2 {
				e = ctx.reject(errf(InvalidArgument, "group must keep 2..16 volumes"))
				return
			}
			remove := make(map[string]bool, len(volumeIDs))
			for _, vid := range volumeIDs {
				remove[vid] = true
			}
			kept := grp.members[:0]
			for _, v := range grp.members {
				if remove[v.id] {
					delete(c.volumes, v.id)
					delete(grp.index, v.id)
					continue
				}
				kept = append(kept, v)
			}
			grp.members = kept
			ctx.ok(fmt.Sprintf("%d members removed", len(volumeIDs)))
		})
	if e0 != nil {
		return e0
	}
	if e != nil {
		return e
	}
	return nil
}

// sortedKeys is a deterministic helper for read views.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
