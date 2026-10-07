package ontology

import (
	"errors"
	"sync"
)

var (
	// ErrEmptyID is returned when a required identifier is empty.
	ErrEmptyID = errors.New("ontology: empty identifier")
	// ErrUnknownGroup is returned when the group does not exist.
	ErrUnknownGroup = errors.New("ontology: unknown permission group")
	// ErrUnknownSubject is returned when the subject has no membership
	// record and the caller required one.
	ErrUnknownSubject = errors.New("ontology: unknown subject")
)

// ValidSubject reports whether a subject identifier is legal. The identifier
// must be non-empty; this check outranks every layer in the overlay rules.
func ValidSubject(s SubjectID) bool { return s != "" }

// Decision is a single permission declaration.
type Decision uint8

const (
	// Unset means the layer declares nothing.
	Unset Decision = iota
	// Allow permits traversal.
	Allow
	// Deny forbids traversal.
	Deny
)

func (d Decision) String() string {
	switch d {
	case Allow:
		return "allow"
	case Deny:
		return "deny"
	default:
		return "unset"
	}
}

type (
	// SubjectID identifies a permission subject (caller).
	SubjectID string
	// GroupID identifies a permission group.
	GroupID string
	// ObjectType is the type of an ontology object.
	ObjectType string
	// LinkType is the type of an ontology link.
	LinkType string
)

// Group is a permission group with a numeric priority (larger wins).
type Group struct {
	ID       GroupID
	Priority int
}

// PermissionState holds groups, subject memberships and the two declaration
// layers. It is safe for concurrent use. Every mutating call produces a new
// immutable snapshot; in-flight queries keep using the snapshot they pinned at
// start.
type PermissionState struct {
	mu sync.RWMutex

	groups      map[GroupID]Group
	membership  map[SubjectID]map[GroupID]struct{}
	objectDecls map[GroupID]map[ObjectType]Decision
	linkDecls   map[GroupID]map[LinkType]Decision
	version     int64
	current     *snapshot
}

// snapshot is an immutable point-in-time view of the permission state.
type snapshot struct {
	version     int64
	groups      map[GroupID]Group
	membership  map[SubjectID]map[GroupID]struct{}
	objectDecls map[GroupID]map[ObjectType]Decision
	linkDecls   map[GroupID]map[LinkType]Decision
}

// NewPermissionState creates an empty permission state.
func NewPermissionState() *PermissionState {
	ps := &PermissionState{
		groups:      map[GroupID]Group{},
		membership:  map[SubjectID]map[GroupID]struct{}{},
		objectDecls: map[GroupID]map[ObjectType]Decision{},
		linkDecls:   map[GroupID]map[LinkType]Decision{},
	}
	ps.current = ps.freezeLocked()
	return ps
}

// freezeLocked builds an immutable deep copy of the current state.
func (ps *PermissionState) freezeLocked() *snapshot {
	s := &snapshot{
		version:     ps.version,
		groups:      make(map[GroupID]Group, len(ps.groups)),
		membership:  make(map[SubjectID]map[GroupID]struct{}, len(ps.membership)),
		objectDecls: make(map[GroupID]map[ObjectType]Decision, len(ps.objectDecls)),
		linkDecls:   make(map[GroupID]map[LinkType]Decision, len(ps.linkDecls)),
	}
	for id, g := range ps.groups {
		s.groups[id] = g
	}
	for subject, groups := range ps.membership {
		cp := make(map[GroupID]struct{}, len(groups))
		for g := range groups {
			cp[g] = struct{}{}
		}
		s.membership[subject] = cp
	}
	deepCopyObjectDecls(ps.objectDecls, s.objectDecls)
	deepCopyLinkDecls(ps.linkDecls, s.linkDecls)
	return s
}

func deepCopyObjectDecls(src map[GroupID]map[ObjectType]Decision, dst map[GroupID]map[ObjectType]Decision) {
	for g, m := range src {
		cp := make(map[ObjectType]Decision, len(m))
		for t, d := range m {
			cp[t] = d
		}
		dst[g] = cp
	}
}

func deepCopyLinkDecls(src map[GroupID]map[LinkType]Decision, dst map[GroupID]map[LinkType]Decision) {
	for g, m := range src {
		cp := make(map[LinkType]Decision, len(m))
		for t, d := range m {
			cp[t] = d
		}
		dst[g] = cp
	}
}

func (ps *PermissionState) mutate(fn func()) *snapshot {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	// Version must be incremented before freezing so the snapshot label and
	// its content describe the same state.
	ps.version++
	fn()
	ps.current = ps.freezeLocked()
	return ps.current
}

// UpsertGroup creates or replaces a permission group and its priority.
func (ps *PermissionState) UpsertGroup(id GroupID, priority int) error {
	if id == "" {
		return ErrEmptyID
	}
	ps.mutate(func() {
		ps.groups[id] = Group{ID: id, Priority: priority}
	})
	return nil
}

// RemoveGroup deletes the group, its memberships and its declarations.
func (ps *PermissionState) RemoveGroup(id GroupID) error {
	if id == "" {
		return ErrEmptyID
	}
	ps.mutate(func() {
		delete(ps.groups, id)
		delete(ps.objectDecls, id)
		delete(ps.linkDecls, id)
		for subject, groups := range ps.membership {
			delete(groups, id)
			if len(groups) == 0 {
				delete(ps.membership, subject)
			}
		}
	})
	return nil
}

// AddMember makes subject a member of group, creating the membership record.
func (ps *PermissionState) AddMember(subject SubjectID, group GroupID) error {
	if subject == "" {
		return ErrEmptyID
	}
	ps.mutate(func() {
		groups := ps.membership[subject]
		if groups == nil {
			groups = map[GroupID]struct{}{}
			ps.membership[subject] = groups
		}
		groups[group] = struct{}{}
	})
	return nil
}

// RemoveMember removes group from subject's membership set.
func (ps *PermissionState) RemoveMember(subject SubjectID, group GroupID) error {
	if subject == "" {
		return ErrEmptyID
	}
	ps.mutate(func() {
		if groups := ps.membership[subject]; groups != nil {
			delete(groups, group)
			if len(groups) == 0 {
				delete(ps.membership, subject)
			}
		}
	})
	return nil
}

// SetObjectDecl sets the object-type-layer declaration of group for t.
// Allow and Deny store a declaration; Unset deletes it.
func (ps *PermissionState) SetObjectDecl(group GroupID, t ObjectType, d Decision) error {
	if group == "" || t == "" {
		return ErrEmptyID
	}
	if d != Allow && d != Deny && d != Unset {
		return errors.New("ontology: invalid decision")
	}
	ps.mutate(func() {
		if d == Unset {
			if m := ps.objectDecls[group]; m != nil {
				delete(m, t)
			}
			return
		}
		m := ps.objectDecls[group]
		if m == nil {
			m = map[ObjectType]Decision{}
			ps.objectDecls[group] = m
		}
		m[t] = d
	})
	return nil
}

// SetLinkDecl sets the link-type-layer declaration of group for lt.
// Allow and Deny store a declaration; Unset deletes it.
func (ps *PermissionState) SetLinkDecl(group GroupID, lt LinkType, d Decision) error {
	if group == "" || lt == "" {
		return ErrEmptyID
	}
	if d != Allow && d != Deny && d != Unset {
		return errors.New("ontology: invalid decision")
	}
	ps.mutate(func() {
		if d == Unset {
			if m := ps.linkDecls[group]; m != nil {
				delete(m, lt)
			}
			return
		}
		m := ps.linkDecls[group]
		if m == nil {
			m = map[LinkType]Decision{}
			ps.linkDecls[group] = m
		}
		m[lt] = d
	})
	return nil
}

// Snapshot returns the immutable state version pinned by a query at start.
func (ps *PermissionState) Snapshot() *snapshot {
	// Mutations replace the current pointer; take the exclusive lock so the
	// pointer load is synchronized with those stores.
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.current
}
