// Package flow implements a single weighted WFQ flow: its weight, its
// virtual finish counter F and a FIFO packet queue whose finish times are
// strictly ascending. It depends on no other package.
package flow

import "errors"

// ErrBadWeight is returned when a flow is created with weight < 1.
var ErrBadWeight = errors.New("flow: weight must be >= 1")

// Packet is one enqueued packet: its byte size and virtual finish time.
type Packet struct {
	Size   int
	Finish int
}

// Flow is one weighted flow.
type Flow struct {
	weight int
	finish int // F_f: finish time of the most recently enqueued packet
	queue  []Packet
}

// New creates a flow with the given weight (must be >= 1).
func New(weight int) (*Flow, error) {
	if weight < 1 {
		return nil, ErrBadWeight
	}
	return &Flow{weight: weight}, nil
}

// Weight returns the flow's weight.
func (f *Flow) Weight() int { return f.weight }

// Finish returns the flow's current virtual finish counter F_f.
func (f *Flow) Finish() int { return f.finish }

// Len returns the number of queued packets.
func (f *Flow) Len() int { return len(f.queue) }

// Enqueue computes F_f = max(F_f, V) + ceil(size/weight), appends the
// packet to the tail and returns it. Because inc >= 1, finish times within
// the flow are strictly ascending. Caller must guarantee size >= 1.
func (f *Flow) Enqueue(size, virtualTime int) Packet {
	inc := (size + f.weight - 1) / f.weight
	f.finish = max(f.finish, virtualTime) + inc
	p := Packet{Size: size, Finish: f.finish}
	f.queue = append(f.queue, p)
	return p
}

// Head returns the head packet (smallest finish time) without removing it.
func (f *Flow) Head() Packet { return f.queue[0] }

// At returns the packet at queue index j (0 = head) without removing it.
func (f *Flow) At(j int) Packet { return f.queue[j] }

// Pop removes and returns the head packet.
func (f *Flow) Pop() Packet {
	p := f.queue[0]
	f.queue = f.queue[1:]
	return p
}
