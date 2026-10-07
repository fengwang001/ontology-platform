package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// Engine holds the declaration state (the single source of truth) and the
// materialized derived state used to answer access checks.
//
// Concurrency model: every mutation takes the write lock and recomputes all
// derived state before returning; every check takes the read lock. Any
// concurrent execution is therefore equivalent to some serial order, and no
// intermediate state of a recomputation is ever observable.
//
// Derived state is a pure function of the declaration state, so the result
// of any sequence of incremental mutations is identical to rebuilding from
// scratch over the current declarations.
type Engine struct {
	mu sync.RWMutex

	// ---- declaration state (source of truth) ----
	objectTypes  map[string]struct{}
	linkTypes    map[string]LinkType
	tags         map[string]struct{}
	roleParents  map[string]map[string]struct{} // role -> parent roles
	subjectRoles map[string]map[string]struct{} // subject -> roles
	instances    map[string]string              // instance -> object type
	attachments  map[string]map[string]struct{} // object type -> directly attached tags
	propagations map[propKey]map[Direction]bool // (tag, link) -> declared directions
	blocks       map[blockKey]struct{}          // (tag, link) blocking points
	grants       map[grantKey]Effect            // (role, tag) -> direct effect

	// ---- derived state (recomputed on every mutation) ----
	typeTags    map[string]map[string]struct{} // object type -> effective tags
	typeBlocked map[string]map[string]struct{} // object type -> tags blocked on every path
	roleGrants  map[string]map[string]GrantInfo
	roleCyclic  map[string]bool

	audit AuditLogger
}

// Option configures an Engine.
type Option func(*Engine)

// WithAuditLogger installs an audit logger. The default is a NopLogger.
func WithAuditLogger(l AuditLogger) Option {
	return func(e *Engine) { e.audit = l }
}

// NewEngine creates an empty engine.
func NewEngine(opts ...Option) *Engine {
	e := &Engine{
		objectTypes:  map[string]struct{}{},
		linkTypes:    map[string]LinkType{},
		tags:         map[string]struct{}{},
		roleParents:  map[string]map[string]struct{}{},
		subjectRoles: map[string]map[string]struct{}{},
		instances:    map[string]string{},
		attachments:  map[string]map[string]struct{}{},
		propagations: map[propKey]map[Direction]bool{},
		blocks:       map[blockKey]struct{}{},
		grants:       map[grantKey]Effect{},
		audit:        NopLogger{},
	}
	for _, o := range opts {
		o(e)
	}
	e.recomputeLocked()
	return e
}

var (
	ErrObjectTypeExists   = errors.New("object type already declared")
	ErrObjectTypeUnknown  = errors.New("object type not declared")
	ErrLinkTypeExists     = errors.New("link type already declared")
	ErrLinkTypeUnknown    = errors.New("link type not declared")
	ErrTagExists          = errors.New("tag already declared")
	ErrTagUnknown         = errors.New("tag not declared")
	ErrRoleUnknown        = errors.New("role not declared")
	ErrSubjectUnknown     = errors.New("subject not declared")
	ErrInstanceExists     = errors.New("instance already declared")
	ErrInstanceUnknown    = errors.New("instance not declared")
	ErrPropagationUnknown = errors.New("propagation declaration not found")
)

// recomputeLocked rebuilds all derived state from the declaration state.
// Caller must hold the write lock. Because derived state is recomputed from
// scratch, incremental maintenance can never drift from a full rebuild.
func (e *Engine) recomputeLocked() {
	e.recomputeTagsLocked()
	e.recomputeGrantsLocked()
}

// mutate runs fn under the write lock, recomputes derived state and records
// an audit event. The recomputation is part of the same critical section, so
// the mutation is atomic: no partially updated state is observable.
func (e *Engine) mutate(op string, input any, fn func() error) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := fn()
	if err == nil {
		e.recomputeLocked()
	}
	e.audit.Log(AuditEvent{Op: op, Input: input, Err: errString(err)})
	return err
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// DeclareObjectType registers an object type.
func (e *Engine) DeclareObjectType(id string) error {
	return e.mutate("DeclareObjectType", id, func() error {
		if _, ok := e.objectTypes[id]; ok {
			return ErrObjectTypeExists
		}
		e.objectTypes[id] = struct{}{}
		return nil
	})
}

// DeclareLinkType registers a link type from one object type to another.
func (e *Engine) DeclareLinkType(id, from, to string) error {
	return e.mutate("DeclareLinkType", LinkType{ID: id, From: from, To: to}, func() error {
		if _, ok := e.linkTypes[id]; ok {
			return ErrLinkTypeExists
		}
		if _, ok := e.objectTypes[from]; !ok {
			return fmt.Errorf("from: %w", ErrObjectTypeUnknown)
		}
		if _, ok := e.objectTypes[to]; !ok {
			return fmt.Errorf("to: %w", ErrObjectTypeUnknown)
		}
		e.linkTypes[id] = LinkType{ID: id, From: from, To: to}
		return nil
	})
}

// RemoveLinkType deletes a link type and every declaration that references
// it (propagations and blocking points). The cascade is atomic.
func (e *Engine) RemoveLinkType(id string) error {
	return e.mutate("RemoveLinkType", id, func() error {
		if _, ok := e.linkTypes[id]; !ok {
			return ErrLinkTypeUnknown
		}
		delete(e.linkTypes, id)
		for k := range e.propagations {
			if k.link == id {
				delete(e.propagations, k)
			}
		}
		for k := range e.blocks {
			if k.link == id {
				delete(e.blocks, k)
			}
		}
		return nil
	})
}

// DeclareTag registers a tag.
func (e *Engine) DeclareTag(id string) error {
	return e.mutate("DeclareTag", id, func() error {
		if _, ok := e.tags[id]; ok {
			return ErrTagExists
		}
		e.tags[id] = struct{}{}
		return nil
	})
}

// DeclareRole registers a role. parentIDs may be empty; parents must already
// exist. Cycles in the role hierarchy are not rejected here; they are
// detected during recomputation and reported at decision time.
func (e *Engine) DeclareRole(id string, parentIDs ...string) error {
	return e.mutate("DeclareRole", map[string]any{"role": id, "parents": parentIDs}, func() error {
		if _, ok := e.roleParents[id]; ok {
			return fmt.Errorf("role %q already declared", id)
		}
		for _, p := range parentIDs {
			if _, ok := e.roleParents[p]; !ok {
				return fmt.Errorf("parent %q: %w", p, ErrRoleUnknown)
			}
		}
		e.roleParents[id] = map[string]struct{}{}
		for _, p := range parentIDs {
			e.roleParents[id][p] = struct{}{}
		}
		return nil
	})
}

// DeclareSubject registers a subject holding the given roles.
func (e *Engine) DeclareSubject(id string, roleIDs ...string) error {
	return e.mutate("DeclareSubject", map[string]any{"subject": id, "roles": roleIDs}, func() error {
		if _, ok := e.subjectRoles[id]; ok {
			return fmt.Errorf("subject %q already declared", id)
		}
		for _, r := range roleIDs {
			if _, ok := e.roleParents[r]; !ok {
				return fmt.Errorf("role %q: %w", r, ErrRoleUnknown)
			}
		}
		e.subjectRoles[id] = map[string]struct{}{}
		for _, r := range roleIDs {
			e.subjectRoles[id][r] = struct{}{}
		}
		return nil
	})
}

// AddRoleParent adds a parent edge to an existing role at runtime. The edge
// may introduce an inheritance cycle; the cycle is detected during
// recomputation and reported as the role-cycle error class at decision time.
func (e *Engine) AddRoleParent(role, parent string) error {
	input := map[string]string{"role": role, "parent": parent}
	return e.mutate("AddRoleParent", input, func() error {
		if _, ok := e.roleParents[role]; !ok {
			return ErrRoleUnknown
		}
		if _, ok := e.roleParents[parent]; !ok {
			return fmt.Errorf("parent %q: %w", parent, ErrRoleUnknown)
		}
		e.roleParents[role][parent] = struct{}{}
		return nil
	})
}

// RemoveRoleParent removes a parent edge from a role.
func (e *Engine) RemoveRoleParent(role, parent string) error {
	input := map[string]string{"role": role, "parent": parent}
	return e.mutate("RemoveRoleParent", input, func() error {
		if _, ok := e.roleParents[role]; !ok {
			return ErrRoleUnknown
		}
		delete(e.roleParents[role], parent)
		return nil
	})
}

// DeclareInstance registers an instance belonging to an object type.
func (e *Engine) DeclareInstance(id, objectType string) error {
	return e.mutate("DeclareInstance", map[string]string{"instance": id, "objectType": objectType}, func() error {
		if _, ok := e.instances[id]; ok {
			return ErrInstanceExists
		}
		if _, ok := e.objectTypes[objectType]; !ok {
			return ErrObjectTypeUnknown
		}
		e.instances[id] = objectType
		return nil
	})
}

// AttachTag attaches a tag directly to an object type.
func (e *Engine) AttachTag(tag, objectType string) error {
	return e.mutate("AttachTag", map[string]string{"tag": tag, "objectType": objectType}, func() error {
		if _, ok := e.tags[tag]; !ok {
			return ErrTagUnknown
		}
		if _, ok := e.objectTypes[objectType]; !ok {
			return ErrObjectTypeUnknown
		}
		set := e.attachments[objectType]
		if set == nil {
			set = map[string]struct{}{}
			e.attachments[objectType] = set
		}
		set[tag] = struct{}{}
		return nil
	})
}

// DetachTag removes a direct tag attachment from an object type. Tags that
// were inherited solely through this source disappear downstream; tags that
// still have independent sources remain. The revocation is atomic.
func (e *Engine) DetachTag(tag, objectType string) error {
	return e.mutate("DetachTag", map[string]string{"tag": tag, "objectType": objectType}, func() error {
		if set := e.attachments[objectType]; set != nil {
			delete(set, tag)
		}
		return nil
	})
}

// DeclarePropagation declares that tag propagates along linkType in dir.
func (e *Engine) DeclarePropagation(tag, linkType string, dir Direction) error {
	input := map[string]string{"tag": tag, "linkType": linkType, "direction": dir.String()}
	return e.mutate("DeclarePropagation", input, func() error {
		if _, ok := e.tags[tag]; !ok {
			return ErrTagUnknown
		}
		if _, ok := e.linkTypes[linkType]; !ok {
			return ErrLinkTypeUnknown
		}
		k := propKey{tag: tag, link: linkType}
		dirs := e.propagations[k]
		if dirs == nil {
			dirs = map[Direction]bool{}
			e.propagations[k] = dirs
		}
		dirs[dir] = true
		return nil
	})
}

// RevokePropagation removes the propagation qualification of a link type
// for a tag (both directions). Tags inherited solely through it disappear
// downstream; independent sources are unaffected. Atomic.
func (e *Engine) RevokePropagation(tag, linkType string) error {
	input := map[string]string{"tag": tag, "linkType": linkType}
	return e.mutate("RevokePropagation", input, func() error {
		k := propKey{tag: tag, link: linkType}
		if _, ok := e.propagations[k]; !ok {
			return ErrPropagationUnknown
		}
		delete(e.propagations, k)
		return nil
	})
}

// DeclareBlock declares a blocking point: tag must not propagate across
// linkType. Paths passing this point stop; other paths are unaffected.
func (e *Engine) DeclareBlock(tag, linkType string) error {
	input := map[string]string{"tag": tag, "linkType": linkType}
	return e.mutate("DeclareBlock", input, func() error {
		if _, ok := e.tags[tag]; !ok {
			return ErrTagUnknown
		}
		if _, ok := e.linkTypes[linkType]; !ok {
			return ErrLinkTypeUnknown
		}
		e.blocks[blockKey{tag: tag, link: linkType}] = struct{}{}
		return nil
	})
}

// RemoveBlock removes a blocking point.
func (e *Engine) RemoveBlock(tag, linkType string) error {
	input := map[string]string{"tag": tag, "linkType": linkType}
	return e.mutate("RemoveBlock", input, func() error {
		delete(e.blocks, blockKey{tag: tag, link: linkType})
		return nil
	})
}

// SetGrant declares a direct allow or deny of a tag for a role.
func (e *Engine) SetGrant(role, tag string, effect Effect) error {
	input := map[string]string{"role": role, "tag": tag, "effect": effect.String()}
	return e.mutate("SetGrant", input, func() error {
		if _, ok := e.roleParents[role]; !ok {
			return ErrRoleUnknown
		}
		if _, ok := e.tags[tag]; !ok {
			return ErrTagUnknown
		}
		e.grants[grantKey{role: role, tag: tag}] = effect
		return nil
	})
}

// UnsetGrant removes a direct grant declaration.
func (e *Engine) UnsetGrant(role, tag string) error {
	input := map[string]string{"role": role, "tag": tag}
	return e.mutate("UnsetGrant", input, func() error {
		delete(e.grants, grantKey{role: role, tag: tag})
		return nil
	})
}
