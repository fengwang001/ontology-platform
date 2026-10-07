package export

import "fmt"

// Position is a logical position in the ontology write log.
type Position int64

// Write is a single ontology write identified by a globally unique ID.
type Write struct {
	Seq Position
	ID  string
}

// Range declares the half-open logical interval (Start, End] of a cycle.
type Range struct {
	Start Position
	End   Position
}

func (r Range) valid() error {
	if r.Start < 0 || r.End < 0 {
		return fmt.Errorf("export: negative position in range [%d,%d]", r.Start, r.End)
	}
	if r.Start > r.End {
		return fmt.Errorf("export: start %d is beyond end %d", r.Start, r.End)
	}
	return nil
}

// Contains reports whether the half-open interval (Start, End] includes seq.
func (r Range) Contains(seq Position) bool { return seq > r.Start && seq <= r.End }

// Sink is a consumer of one link's output. Implementations must be idempotent
// per Write.ID: after an interrupted, unconfirmed cycle the same writes may be
// handed over again from the last confirmed start.
type Sink interface {
	Output(w Write) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(Write) error

func (f SinkFunc) Output(w Write) error { return f(w) }
