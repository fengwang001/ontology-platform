// Package ontology implements an incremental view maintenance subsystem for
// an ontology platform: a view aggregates objects connected through a link
// relation, groups them by a time-typed property, and keeps grouping and
// ordering stable across default-timezone definition version migrations.
package ontology

import (
	"fmt"
	"time"
)

// WallClock is a civil (zone-less) date-time as written by a client. Its
// meaning (which instant it denotes) is fixed only after pairing it with the
// default-timezone definition version anchored at write time.
type WallClock struct {
	t time.Time // always kept in time.UTC; fields carry the civil value
}

// WallClockLayout is the accepted wire format for WallClock.
const WallClockLayout = "2006-01-02T15:04:05"

// ParseWall parses a civil time such as "2026-03-01T09:30:00".
func ParseWall(s string) (WallClock, error) {
	t, err := time.ParseInLocation(WallClockLayout, s, time.UTC)
	if err != nil {
		return WallClock{}, fmt.Errorf("ontology: bad wall clock %q: %w", s, err)
	}
	return WallClock{t: t}, nil
}

// MustWall is a test/demo helper that panics on malformed input.
func MustWall(s string) WallClock {
	w, err := ParseWall(s)
	if err != nil {
		panic(err)
	}
	return w
}

func (w WallClock) String() string { return w.t.Format(WallClockLayout) }

// TZVersion is one accepted version of an object type's default-timezone
// definition. Versions are strictly increasing per object type, starting at 1.
type TZVersion struct {
	Version int
	Zone    string // IANA zone name, e.g. "Asia/Shanghai"
}

// WriteRec is the write-time anchored record of a time property value. The
// TZVersion field pins which default-timezone definition version was in
// effect when the value was written; 0 means the object type had no default
// timezone defined at that moment.
type WriteRec struct {
	ObjID     string
	TypeID    string
	Prop      string
	Wall      WallClock
	WriteSeq  uint64 // store-assigned, monotonic; last-writer-wins key
	TZVersion int
}

// EventKind classifies change events delivered to views.
type EventKind int

const (
	EvWrite  EventKind = iota // a time property value was written
	EvLink                    // two objects were linked
	EvUnlink                  // a link was removed
	// EvTypeChanged marks an object type metadata change (property
	// deprecation or type deletion) that views must observe.
	EvTypeChanged
)

func (k EventKind) String() string {
	switch k {
	case EvWrite:
		return "write"
	case EvLink:
		return "link"
	case EvUnlink:
		return "unlink"
	case EvTypeChanged:
		return "type-changed"
	}
	return "unknown"
}

// Event is a self-contained change record. Because every event carries its
// full write-time anchored payload, views can apply events in any arrival
// order and still converge to the same state.
type Event struct {
	LogSeq uint64 // position in the delivery log, assigned at dispatch
	Kind   EventKind

	// EvWrite payload.
	Write WriteRec

	// EvLink / EvUnlink payload.
	LinkType  string
	LeftID    string
	LeftType  string
	RightID   string
	RightType string

	// EvTypeChanged payload.
	ChangedType string
}

// Group is one bucket of a view query result.
type Group struct {
	Key   string // UTC calendar day, e.g. "2026-03-01"
	Items []Item
}

// Item is one grouped object in a view query result.
type Item struct {
	ObjID      string
	TypeID     string
	Normalized time.Time // UTC instant after normalization
	TZVersion  int       // timezone definition version this placement used
	GroupKey   string
}
