package ontology

import "fmt"

// Property maps a property name to its value. Values are treated as
// opaque scalars; the ontology layer only compares them for equality.
type Property map[string]any

// ObjectTypeName identifies a registered object type.
type ObjectTypeName string

// LinkTypeName identifies a registered link type.
type LinkTypeName string

// InstanceID identifies an object instance inside the store.
type InstanceID string

// Version is the monotonically increasing per-instance optimistic-concurrency
// token. A newly created instance has version 1; every committed batch item
// touching an instance increments it by exactly one.
type Version int64

// Cardinality bounds how many link edges of a given link type may touch one
// endpoint. Zero means "unbounded".
type Cardinality int

// LinkType describes a directed association between two object types.
// SrcMax bounds the number of outgoing edges per source instance and
// DstMax bounds the number of incoming edges per target instance.
type LinkType struct {
	Name   LinkTypeName
	Source ObjectTypeName
	Target ObjectTypeName
	SrcMax Cardinality
	DstMax Cardinality
}

// ValidationHook inspects a proposed instance change in the context of the
// complete post-batch image and returns a non-empty rejection reason if the
// change must not commit. Hooks must be pure: they may not mutate state and
// must be deterministic, which makes recorded batches replayable.
//
// proposed is the image the instance would have if the whole batch commits;
// view exposes the post-batch image of every instance involved in the batch.
type ValidationHook struct {
	// Run is the hook body.
	Run func(proposed *Instance, view BatchView) (rejectReason string)
}

// ObjectType is the schema of one kind of object instance.
type ObjectType struct {
	Name     ObjectTypeName
	Validate ValidationHook
}

// Registry is the schema registry used by a store.
type Registry struct {
	objects map[ObjectTypeName]*ObjectType
	links   map[LinkTypeName]*LinkType
}

// NewRegistry creates an empty schema registry.
func NewRegistry() *Registry {
	return &Registry{
		objects: map[ObjectTypeName]*ObjectType{},
		links:   map[LinkTypeName]*LinkType{},
	}
}

// RegisterObjectType adds an object type. Registering a type twice panics:
// schema registration is a bootstrap-time error, not a batch failure.
func (r *Registry) RegisterObjectType(t ObjectType) *Registry {
	if _, ok := r.objects[t.Name]; ok {
		panic(fmt.Sprintf("ontology: duplicate object type %q", t.Name))
	}
	cp := t
	r.objects[cp.Name] = &cp
	return r
}

// RegisterLinkType adds a link type.
func (r *Registry) RegisterLinkType(t LinkType) *Registry {
	if _, ok := r.links[t.Name]; ok {
		panic(fmt.Sprintf("ontology: duplicate link type %q", t.Name))
	}
	cp := t
	r.links[cp.Name] = &cp
	return r
}

func (r *Registry) object(name ObjectTypeName) (*ObjectType, bool) {
	t, ok := r.objects[name]
	return t, ok
}

func (r *Registry) link(name LinkTypeName) (*LinkType, bool) {
	t, ok := r.links[name]
	return t, ok
}

// Instance is a stored object instance.
type Instance struct {
	ID         InstanceID
	Type       ObjectTypeName
	Version    Version
	Properties Property
}

func (i *Instance) clone() *Instance {
	cp := *i
	cp.Properties = cloneProperties(i.Properties)
	return &cp
}

func cloneProperties(p Property) Property {
	if p == nil {
		return nil
	}
	cp := make(Property, len(p))
	for k, v := range p {
		cp[k] = v
	}
	return cp
}

// Edge is one directed link edge.
type Edge struct {
	Link   LinkTypeName
	Source InstanceID
	Target InstanceID
}

// EdgeDelta mutates one link edge. Add=true inserts the edge, Add=false
// removes it.
type EdgeDelta struct {
	Edge Edge
	Add  bool
}
