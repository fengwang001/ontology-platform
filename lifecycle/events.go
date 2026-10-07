package lifecycle

import "time"

// EventKind enumerates the committed lifecycle event types.
type EventKind int

const (
	EventInitial EventKind = iota
	EventExpiry
	EventForced
	EventAction
	EventProperty
	EventLink
)

// Event is one immutable entry in an instance's lifecycle history.
type Event struct {
	Seq        int64
	At         time.Time
	OccurredAt time.Time // expiry/forced rings: virtual due instant; else At
	TypeName   string    // initial events: object type of the instance
	Name       string    // property/link events: slot name
	Value      string    // property/link events: new value
	Kind       EventKind
	State      string
	Prev       string
	Transition string
	Detail     string
}
