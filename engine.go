package ontology

import (
	"fmt"
	"sort"
	"sync"
	"time"

	_ "time/tzdata" // guarantee IANA zones load even without system tzdata
)

// objectType is the engine's authoritative record of an object type.
type objectType struct {
	id           string
	tzChain      []TZVersion // accepted versions only; index = Version-1
	props        map[string]bool
	groupingProp string
	typeVersion  int
	deleted      bool
}

// linkType records which object types a link relation connects.
type linkType struct {
	id        string
	leftType  string
	rightType string
}

// Engine serializes every operation (writes, migrations, view maintenance,
// queries) through one mutex, so any concurrent mix is observably equivalent
// to the global serial order in which the mutex was acquired.
type Engine struct {
	mu    sync.Mutex
	seq   uint64 // global serial number, also the write sequence source
	logn  uint64 // delivery log position
	types map[string]*objectType
	links map[string]*linkType
	log   []Event // authoritative delivery log views sync from
	views map[string]*View
}

// NewEngine creates an empty engine.
func NewEngine() *Engine {
	return &Engine{
		types: make(map[string]*objectType),
		links: make(map[string]*linkType),
		views: make(map[string]*View),
	}
}

// nextSeq advances the global serial order. Caller must hold e.mu.
func (e *Engine) nextSeq() uint64 {
	e.seq++
	return e.seq
}

// DefineObjectType registers an object type with its grouping time property.
func (e *Engine) DefineObjectType(id, groupingProp string, timeProps ...string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.types[id]; ok {
		return fmt.Errorf("ontology: object type %q already defined", id)
	}
	props := make(map[string]bool, len(timeProps))
	for _, p := range timeProps {
		props[p] = true
	}
	if !props[groupingProp] {
		return fmt.Errorf("ontology: grouping property %q not declared", groupingProp)
	}
	e.types[id] = &objectType{
		id:           id,
		props:        props,
		groupingProp: groupingProp,
		typeVersion:  1,
	}
	return nil
}

// DefineTimezone installs the first default-timezone definition (version 1).
func (e *Engine) DefineTimezone(typeID, zone string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeID]
	if !ok || t.deleted {
		return fmt.Errorf("ontology: object type %q not found", typeID)
	}
	if len(t.tzChain) != 0 {
		return fmt.Errorf("ontology: type %q already has a timezone definition", typeID)
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return fmt.Errorf("ontology: invalid zone %q: %w", zone, err)
	}
	t.tzChain = append(t.tzChain, TZVersion{Version: 1, Zone: zone})
	return nil
}

// MigrateTimezone moves a type's default timezone to a new version. The
// migration is validated: the type must exist, expectedBase must equal the
// current version (strict +1 progression), and the zone must be valid. A
// rejected migration consumes no version number; writes anchored to the
// rejected version surface as ErrMigrationInvalid during view maintenance.
func (e *Engine) MigrateTimezone(typeID, zone string, expectedBase int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeID]
	if !ok || t.deleted {
		return fmt.Errorf("%w: object type %q not found", ErrMigrationFailed{Zone: zone}, typeID)
	}
	if got := len(t.tzChain); expectedBase != got {
		return ErrMigrationFailed{
			Zone:   zone,
			Reason: fmt.Sprintf("expected base version %d, current is %d", expectedBase, got),
		}
	}
	if _, err := time.LoadLocation(zone); err != nil {
		return ErrMigrationFailed{Zone: zone, Reason: err.Error()}
	}
	t.tzChain = append(t.tzChain, TZVersion{Version: len(t.tzChain) + 1, Zone: zone})
	return nil
}

// ErrMigrationFailed reports a rejected timezone definition migration.
type ErrMigrationFailed struct {
	Zone   string
	Reason string
}

func (e ErrMigrationFailed) Error() string {
	msg := "ontology: timezone migration to " + e.Zone + " rejected"
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

// DeprecateProperty removes a property from a type's active set and bumps
// the type version. Objects grouped by that property become unplaceable.
func (e *Engine) DeprecateProperty(typeID, prop string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeID]
	if !ok || t.deleted {
		return fmt.Errorf("ontology: object type %q not found", typeID)
	}
	if !t.props[prop] {
		return fmt.Errorf("ontology: property %q not active on type %q", prop, typeID)
	}
	delete(t.props, prop)
	t.typeVersion++
	e.appendLog(Event{Kind: EvTypeChanged, ChangedType: typeID})
	return nil
}

// DeleteObjectType removes a type. Link relations referencing it keep their
// record, so views over them start reporting ErrLinkEndpointMissing.
func (e *Engine) DeleteObjectType(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[id]
	if !ok || t.deleted {
		return fmt.Errorf("ontology: object type %q not found", id)
	}
	t.deleted = true
	e.appendLog(Event{Kind: EvTypeChanged, ChangedType: id})
	return nil
}

// appendLog appends an internally generated event to the delivery log.
// Caller must hold e.mu.
func (e *Engine) appendLog(ev Event) {
	e.logn++
	ev.LogSeq = e.logn
	e.log = append(e.log, ev)
}

// DefineLinkType registers a link relation between two object types.
func (e *Engine) DefineLinkType(id, leftType, rightType string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.links[id]; ok {
		return fmt.Errorf("ontology: link type %q already defined", id)
	}
	e.links[id] = &linkType{id: id, leftType: leftType, rightType: rightType}
	return nil
}

// Write records a time property value, anchoring it to the timezone
// definition version in effect right now, and returns the change event. The
// event is NOT delivered to any view until Dispatch is called, which lets
// callers hold events back to emulate late arrival.
func (e *Engine) Write(objID, typeID, prop string, wall WallClock) (Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t, ok := e.types[typeID]
	if !ok || t.deleted {
		return Event{}, fmt.Errorf("ontology: object type %q not found", typeID)
	}
	if !t.props[prop] {
		return Event{}, fmt.Errorf("ontology: property %q not active on type %q", prop, typeID)
	}
	seq := e.nextSeq()
	return Event{
		Kind: EvWrite,
		Write: WriteRec{
			ObjID:     objID,
			TypeID:    typeID,
			Prop:      prop,
			Wall:      wall,
			WriteSeq:  seq,
			TZVersion: len(t.tzChain), // 0 means: none defined at write time
		},
	}, nil
}

// WritePinned is like Write but anchors the value to an explicitly chosen
// timezone definition version, emulating a client that pinned a version its
// migration may have failed to establish. Anchoring to a version outside the
// accepted chain is what later surfaces as ErrMigrationInvalid.
func (e *Engine) WritePinned(objID, typeID, prop string, wall WallClock, tzVersion int) (Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.types[typeID]; !ok {
		return Event{}, fmt.Errorf("ontology: object type %q not found", typeID)
	}
	seq := e.nextSeq()
	return Event{
		Kind: EvWrite,
		Write: WriteRec{
			ObjID:     objID,
			TypeID:    typeID,
			Prop:      prop,
			Wall:      wall,
			WriteSeq:  seq,
			TZVersion: tzVersion,
		},
	}, nil
}

// Link connects two objects and returns the (undispatched) event.
func (e *Engine) Link(linkTypeID, leftID, leftType, rightID, rightType string) (Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.links[linkTypeID]; !ok {
		return Event{}, fmt.Errorf("ontology: link type %q not found", linkTypeID)
	}
	e.nextSeq()
	return Event{
		Kind:      EvLink,
		LinkType:  linkTypeID,
		LeftID:    leftID,
		LeftType:  leftType,
		RightID:   rightID,
		RightType: rightType,
	}, nil
}

// Unlink removes a link and returns the (undispatched) event.
func (e *Engine) Unlink(linkTypeID, leftID, leftType, rightID, rightType string) (Event, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nextSeq()
	return Event{
		Kind:      EvUnlink,
		LinkType:  linkTypeID,
		LeftID:    leftID,
		LeftType:  leftType,
		RightID:   rightID,
		RightType: rightType,
	}, nil
}

// Dispatch appends events to the delivery log in the given order. Views pick
// them up on their next Sync. Dispatch order is the arrival order; events
// dispatched late carry their original write-time anchoring regardless.
func (e *Engine) Dispatch(events ...Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range events {
		e.appendLog(ev)
	}
}

// NewView creates a view aggregating objects connected by the given link
// relation.
func (e *Engine) NewView(id, linkTypeID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, ok := e.links[linkTypeID]; !ok {
		return fmt.Errorf("ontology: link type %q not found", linkTypeID)
	}
	if _, ok := e.views[id]; ok {
		return fmt.Errorf("ontology: view %q already exists", id)
	}
	e.views[id] = newView(id, linkTypeID)
	return nil
}

// Sync advances a view over all log events it has not yet applied.
func (e *Engine) Sync(viewID string) (Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.views[viewID]
	if !ok {
		return Report{}, fmt.Errorf("ontology: view %q not found", viewID)
	}
	return v.sync(e), nil
}

// Query synchronizes the view and returns its grouped, ordered content.
func (e *Engine) Query(viewID string) ([]Group, Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.views[viewID]
	if !ok {
		return nil, Report{}, fmt.Errorf("ontology: view %q not found", viewID)
	}
	rep := v.sync(e)
	return v.result(), rep, nil
}

// Audit returns a copy of the view's decision log.
func (e *Engine) Audit(viewID string) ([]AuditEntry, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.views[viewID]
	if !ok {
		return nil, fmt.Errorf("ontology: view %q not found", viewID)
	}
	out := make([]AuditEntry, len(v.audit))
	copy(out, v.audit)
	return out, nil
}

// Log returns a copy of the engine's authoritative delivery log.
func (e *Engine) Log() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Event, len(e.log))
	copy(out, e.log)
	return out
}

// LastQueryOps reports how many comparison/emit operations the most recent
// Query on this view performed. It is the deterministic, independently
// checkable witness that query cost tracks result size, not history size.
func (e *Engine) LastQueryOps(viewID string) (int64, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	v, ok := e.views[viewID]
	if !ok {
		return 0, fmt.Errorf("ontology: view %q not found", viewID)
	}
	return v.lastQueryOps, nil
}

// CurrentTZVersion returns the number of accepted timezone definition
// versions for a type (0 when none is defined).
func (e *Engine) CurrentTZVersion(typeID string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t, ok := e.types[typeID]; ok && !t.deleted {
		return len(t.tzChain)
	}
	return 0
}

// snapshot is the read-only view of engine state the view maintainer uses.
type snapshot struct {
	types map[string]*objectType
	links map[string]*linkType
}

func (e *Engine) snap() snapshot { return snapshot{types: e.types, links: e.links} }

// tzZone resolves an accepted timezone definition version to its zone.
func (s snapshot) tzZone(typeID string, version int) (string, bool) {
	t, ok := s.types[typeID]
	if !ok || t.deleted || version < 1 || version > len(t.tzChain) {
		return "", false
	}
	return t.tzChain[version-1].Zone, true
}

// groupingPropActive reports whether the type's grouping property is still
// active in the current type version.
func (s snapshot) groupingPropActive(typeID string) (prop string, active bool, ok bool) {
	t, exists := s.types[typeID]
	if !exists || t.deleted {
		return "", false, false
	}
	return t.groupingProp, t.props[t.groupingProp], true
}

// linkEndpointsExist reports whether both endpoint types of a link relation
// still exist.
func (s snapshot) linkEndpointsExist(linkTypeID string) bool {
	l, ok := s.links[linkTypeID]
	if !ok {
		return false
	}
	lt, lok := s.types[l.leftType]
	rt, rok := s.types[l.rightType]
	return lok && rok && !lt.deleted && !rt.deleted
}

// sortedKeys is a small helper used by reports and tests.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
