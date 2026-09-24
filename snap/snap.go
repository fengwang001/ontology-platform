// Package snap owns the accumulator, cross-channel barrier alignment, the
// snap[id] table and downstream barrier emission; it depends only on align.
package snap

import (
	"errors"
	"fmt"

	"ontology/align"
)

// The four failure modes are distinct sentinel errors.
var (
	ErrChannels     = errors.New("snap: channels must be positive")
	ErrChannelRange = errors.New("snap: channel out of range")
	ErrBarrierID    = errors.New("snap: barrier id must be positive")
	ErrBarrierOrder = errors.New("snap: barrier id must strictly increase")
)

// Event is a record Rec(Ch, Delta); when IsBar is set it is Bar(Ch, Barrier).
type Event struct {
	Ch, Delta int
	IsBar     bool
	Barrier   int // IsBar: positive, strictly increasing id on that channel
}

// Rec builds a record carrying delta on channel ch.
func Rec(ch, delta int) Event { return Event{Ch: ch, Delta: delta} }

// Bar builds a barrier with id on channel ch.
func Bar(ch, id int) Event { return Event{Ch: ch, IsBar: true, Barrier: id} }

// Engine is the aligned snapshot operator.
type Engine struct {
	chs     []*align.Channel
	snaps   map[int]int
	emitted []int // barriers emitted downstream, in order
	sum     int
	nblock  int // channels blocked on current barrier: O(1) alignment test
	probed  int // unexported: channels inspected by latest completion decision
}

// New builds an engine over n input channels.
func New(n int) (*Engine, error) {
	if n <= 0 {
		return nil, ErrChannels
	}
	e := &Engine{chs: make([]*align.Channel, n), snaps: map[int]int{}}
	for i := range e.chs {
		e.chs[i] = align.NewChannel()
	}
	return e, nil
}

// Sum returns the current accumulator value.
func (e *Engine) Sum() int { return e.sum }

// Snapshot returns the snapshot taken when barrier id completed alignment.
func (e *Engine) Snapshot(id int) (int, bool) { v, ok := e.snaps[id]; return v, ok }

// Apply validates the whole batch first (touching no state), then applies it
// in global arrival order; any rejected batch leaves no trace at all.
func (e *Engine) Apply(evs []Event) error {
	// Validate against each channel's own last id (no mutation): the same
	// epoch id legitimately arrives once on every channel.
	batch := map[int]int{} // ch -> highest barrier id within this batch
	for _, ev := range evs {
		if ev.Ch < 0 || ev.Ch >= len(e.chs) {
			return ErrChannelRange
		}
		if ev.IsBar {
			if ev.Barrier <= 0 {
				return ErrBarrierID
			}
			prev := e.chs[ev.Ch].LastBarrier()
			if b, ok := batch[ev.Ch]; ok {
				prev = b
			}
			if ev.Barrier <= prev {
				return ErrBarrierOrder
			}
			batch[ev.Ch] = ev.Barrier
		}
	}
	for _, ev := range evs {
		c := e.chs[ev.Ch]
		if !ev.IsBar {
			if c.Blocked() {
				c.Hold(ev.Delta) // next epoch: buffer, never touch sum
			} else {
				e.sum += ev.Delta
			}
			continue
		}
		c.MarkBarrier(ev.Barrier)
		c.Block(ev.Barrier)
		e.nblock++
		e.probed = 0 // only the counter is compared: zero channels inspected
		if e.nblock == len(e.chs) {
			e.completeLocked(ev.Barrier)
		}
	}
	return nil
}

// completeLocked fires once all channels are blocked: snapshot FIRST (sum
// still excludes every in-flight delta), then drain buffers in order and
// release the blocks, emitting the barrier downstream.
func (e *Engine) completeLocked(id int) {
	e.snaps[id] = e.sum
	for _, c := range e.chs {
		for _, d := range c.Pending() {
			e.sum += d
		}
		c.Reset()
	}
	e.nblock = 0
	e.emitted = append(e.emitted, id)
}

// SelfCheck replays the eight-event stream, a rejected batch and large m.
// The counter never leaves the package; failure is returned as an error.
func SelfCheck() error {
	evs := []Event{Rec(0, 5), Rec(1, 10), Bar(0, 1), Rec(0, 7), Rec(1, 3),
		Bar(1, 1), Rec(0, 2), Rec(1, 1)}
	want := []int{5, 15, 15, 15, 18, 25, 27, 28}
	e, _ := New(2)
	for i, ev := range evs {
		if err := e.Apply([]Event{ev}); err != nil || e.sum != want[i] {
			return fmt.Errorf("step %d: sum=%d want %d err=%v", i+1, e.sum, want[i], err)
		}
	}
	if v, ok := e.snaps[1]; !ok || v != 18 || e.sum != 28 || len(e.emitted) != 1 {
		return fmt.Errorf("invariants snap=%d(%v) sum=%d", v, ok, e.sum)
	}
	if err := e.Apply([]Event{Rec(0, 4), Bar(0, 0)}); !errors.Is(err, ErrBarrierID) || e.sum != 28 || e.nblock != 0 {
		return errors.New("rejected batch left traces or wrong error")
	}
	for _, m := range []int{100, 1000, 10000} {
		big, _ := New(m)
		bars := make([]Event, m-1)
		for i := range bars {
			bars[i] = Bar(i, 1)
		}
		if big.Apply(bars) != nil || big.Apply([]Event{Bar(m-1, 1)}) != nil || big.probed > 1 {
			return fmt.Errorf("m=%d completion not O(1)", m)
		}
	}
	return nil
}
