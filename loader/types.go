// Package loader implements a browser-style resource loading scheduler:
// request registration, per-origin connection quotas, priority preemption,
// a preload cache and completion notification, all under a logical clock.
package loader

import (
	"errors"
	"fmt"
)

// Error categories, distinguishable via errors.Is.
var (
	ErrInvalidArgument = errors.New("loader: invalid argument")
	ErrClockSkew       = errors.New("loader: clock moved backwards")
	ErrNotFound        = errors.New("loader: request not found")
	ErrInvalidState    = errors.New("loader: operation not allowed in current state")
	ErrAborted         = errors.New("loader: request aborted")
)

// Origin identifies a source by scheme, host and port.
type Origin struct {
	Scheme string
	Host   string
	Port   int
}

// ResourceType enumerates the loadable resource kinds.
type ResourceType int

const (
	TypeDocument ResourceType = iota
	TypeStyle
	TypeScript
	TypeFont
	TypeImage
	TypePreload
)

func (t ResourceType) valid() bool { return t >= TypeDocument && t <= TypePreload }

func (t ResourceType) String() string {
	switch t {
	case TypeDocument:
		return "document"
	case TypeStyle:
		return "style"
	case TypeScript:
		return "script"
	case TypeFont:
		return "font"
	case TypeImage:
		return "image"
	case TypePreload:
		return "preload"
	}
	return "unknown"
}

// ParseResourceType converts a textual type; unknown names are invalid arguments.
func ParseResourceType(s string) (ResourceType, error) {
	for t := TypeDocument; t <= TypePreload; t++ {
		if t.String() == s {
			return t, nil
		}
	}
	return 0, fmt.Errorf("%w: unknown resource type %q", ErrInvalidArgument, s)
}

// Priority: higher value wins; only PriorityHighest may preempt.
type Priority int

const (
	PriorityLowest Priority = iota
	PriorityLow
	PriorityMedium
	PriorityHigh
	PriorityHighest
)

const numPriorities = int(PriorityHighest) + 1

func (p Priority) valid() bool { return p >= PriorityLowest && p <= PriorityHighest }

func (p Priority) String() string {
	names := []string{"lowest", "low", "medium", "high", "highest"}
	if p.valid() {
		return names[p]
	}
	return "invalid"
}

// State is the request lifecycle; terminal states are mutually exclusive
// and irreversible.
type State int

const (
	StatePending State = iota
	StateTransferring
	StateAttached
	StateCompleted
	StateFailed
	StateAborted
)

func (s State) terminal() bool { return s >= StateCompleted }

func (s State) String() string {
	names := []string{"pending", "transferring", "attached", "completed", "failed", "aborted"}
	if s >= StatePending && s <= StateAborted {
		return names[s]
	}
	return "invalid"
}

// RequestInput describes a resource request at registration time.
type RequestInput struct {
	Origin      Origin
	URL         string
	Type        ResourceType
	As          ResourceType // destination type, required when Type == TypePreload
	Priority    Priority
	Credentials string
	Integrity   string
	Size        int64 // total bytes; progress reaching Size completes the request
}

// Config holds the tunable limits. Both limits must be non-zero.
type Config struct {
	PerOriginLimit int   // max concurrent transfers per origin
	GlobalLimit    int   // max concurrent transfers overall
	MaxPauses      int   // max times a single request may be paused
	PreloadTTL     int64 // preload cache time-to-live in clock units
}

// Status is a point-in-time snapshot of a request.
type Status struct {
	State             State
	Progress          int64
	Size              int64
	Pauses            int
	EffectivePriority Priority
	AttachedTo        uint64 // request id of the preload this request rides on, 0 if none
	FromCache         bool
	Cause             error // ErrAborted when the terminal state is an abort
}

// WasteEntry records a preload cache entry that expired unused.
type WasteEntry struct {
	Origin      Origin
	URL         string
	CompletedAt int64
	WastedAt    int64
}

// request is the internal mutable record.
type request struct {
	id         uint64
	seq        uint64 // registration order, never reused
	in         RequestInput
	state      State
	progress   int64
	pauses     int
	startSeq   uint64 // last start order; 0 means never started
	tier       int    // pendingSet tier the request is filed under
	attachers  []*request
	attachedTo *request
	fromCache  bool
	cause      error
}

// effPriority is the request's own priority raised by any attacher.
func (r *request) effPriority() Priority {
	p := r.in.Priority
	for _, a := range r.attachers {
		if a.in.Priority > p {
			p = a.in.Priority
		}
	}
	return p
}
