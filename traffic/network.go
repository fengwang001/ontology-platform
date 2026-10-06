// Package traffic simulates how road incidents reduce link capacity,
// how queues form and spill backwards up a directed road network, how
// multiple incidents combine, how queues dissipate after incidents
// clear, and how the affected region is queried at any time.
//
// All time/flow/queue quantities are exact rationals and the engine is
// event driven, so trajectories are reproduced exactly regardless of
// how many Advance calls partition the timeline. See DESIGN.md for the
// key trade-offs.
package traffic

import "math/big"

// Rat is the time/flow/length/queue type used everywhere in the
// simulation. Exact rational arithmetic guarantees that the same
// operation sequence reproduces byte-identical trajectories no matter
// how many Advance calls slice the timeline.
type Rat = big.Rat

func newRat() *Rat { return new(Rat) }

func ratCopy(x *Rat) *Rat {
	if x == nil {
		return nil
	}
	return newRat().Set(x)
}

// Network is a static directed graph of nodes and links.
type Network struct {
	links    map[string]*Link
	upstream map[string][]string // linkID -> direct upstream link IDs
}

// Link is a static description of a directed road segment.
// Capacity and Arrival share the same per-time unit. Length is in the
// same length unit as the service's vehicle length.
type Link struct {
	ID       string
	From     string
	To       string
	Length   *Rat
	Capacity *Rat
	Arrival  *Rat
}

// NewNetwork returns an empty network.
func NewNetwork() *Network {
	return &Network{
		links:    make(map[string]*Link),
		upstream: make(map[string][]string),
	}
}

// AddLink registers a link. IDs must be non-empty and unique, physical
// fields must be positive, and the constant arrival flow must not
// exceed capacity. Upstream adjacency is derived from node endpoints.
func (n *Network) AddLink(l Link) error {
	if l.ID == "" || l.From == "" || l.To == "" || l.Length == nil || l.Capacity == nil || l.Arrival == nil {
		return ErrInvalidArgument
	}
	if l.Length.Sign() <= 0 || l.Capacity.Sign() <= 0 || l.Arrival.Sign() < 0 {
		return ErrInvalidArgument
	}
	if _, dup := n.links[l.ID]; dup {
		return ErrInvalidArgument
	}
	if l.Arrival.Cmp(l.Capacity) > 0 {
		return ErrArrivalExceedsCap
	}
	cp := &Link{
		ID:       l.ID,
		From:     l.From,
		To:       l.To,
		Length:   ratCopy(l.Length),
		Capacity: ratCopy(l.Capacity),
		Arrival:  ratCopy(l.Arrival),
	}
	n.links[cp.ID] = cp
	n.upstream[cp.ID] = n.upstream[cp.ID] // ensure key present
	return nil
}

// freeze computes direct upstream adjacency from node endpoints.
// Called once by NewService after all links have been added.
func (n *Network) freeze() {
	outgoing := make(map[string][]string)
	for id, l := range n.links {
		outgoing[l.To] = append(outgoing[l.To], id)
	}
	for id, l := range n.links {
		n.upstream[id] = append([]string(nil), outgoing[l.From]...)
	}
}

func (n *Network) link(id string) (*Link, bool) {
	l, ok := n.links[id]
	return l, ok
}

// Incident is a registration of a capacity reduction on one link.
// Ratio in [0,1] multiplies the link capacity (capacity*(1-ratio)).
type Incident struct {
	ID     string
	LinkID string
	Start  *Rat
	Ratio  *Rat
}

// LinkState answers a query at the current simulation time.
// Level 0 means unaffected; levels start at 1 on incident links.
// Queue is measured in vehicles.
type LinkState struct {
	LinkID string
	Queue  *Rat
	Level  int
}
