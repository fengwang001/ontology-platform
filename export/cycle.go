package export

import (
	"context"
	"sort"
	"sync"
)

// Cycle is one export interval from the last confirmed end to a declared end.
// Its end is only confirmed after every expected write has been handed off,
// the epoch is sealed in history, and the checkpoint save succeeds. Until
// then the cycle is unconfirmed: an interruption leaves the checkpoint at the
// previous end and a restart replays the identical interval.
type Cycle struct {
	mu sync.Mutex

	mgr   *Manager
	link  string
	r     Range
	epoch int
	dedup *Deduper
	open  bool
}

// Epoch returns this cycle's history epoch number.
func (c *Cycle) Epoch() int { return c.epoch }

// Range returns the declared interval.
func (c *Cycle) Range() Range { return c.r }

// Accept merges one submitted write into this cycle.
//
// Retries of the same ID are swallowed in place: the write keeps its first
// acceptance slot, regardless of when a retry arrives or which other writes
// arrive between attempts. Writes outside (Start, End] are rejected; this is
// an interval-integrity error, not a resource failure.
func (c *Cycle) Accept(_ context.Context, w Write) (fresh bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.open {
		return false, mkErr(KindResourceExhausted, "link %s: cycle (%d,%d] is no longer open", c.link, c.r.Start, c.r.End)
	}
	if !c.r.Contains(w.Seq) {
		return false, mkErr(KindStartMismatch, "link %s: write seq %d outside declared interval (%d,%d]", c.link, w.Seq, c.r.Start, c.r.End)
	}
	_, fresh = c.dedup.Accept(w)
	return fresh, nil
}

// Deliver hands the de-duplicated, first-acceptance-ordered sequence to the
// sink and appends each handoff to history. A sink failure is classified as
// resource exhaustion: the cycle stays unconfirmed and the next attempt
// replays, relying on sink idempotency by Write.ID.
func (c *Cycle) Deliver(ctx context.Context, sink Sink) ([]Write, error) {
	c.mu.Lock()
	if !c.open {
		c.mu.Unlock()
		return nil, mkErr(KindResourceExhausted, "link %s: cycle already finished", c.link)
	}
	ws := c.dedup.Merged()
	c.mu.Unlock()

	for _, w := range ws {
		if err := ctx.Err(); err != nil {
			return nil, mkErr(KindResourceExhausted, "link %s: interrupted before handoff: %v", c.link, err)
		}
		// History first: a crash between the append and the sink call only
		// risks a duplicate handoff (sink de-dupes), never a lost write.
		if err := c.mgr.history.Append(Record{Link: c.link, Seq: w.Seq, ID: w.ID, Epoch: c.epoch}); err != nil {
			return nil, mkErr(KindResourceExhausted, "link %s: history append failed: %v", c.link, err)
		}
		if err := sink.Output(w); err != nil {
			return nil, mkErr(KindResourceExhausted, "link %s: sink refused write %s: %v", c.link, w.ID, err)
		}
	}
	return ws, nil
}

// Confirm verifies that the whole interval is covered exactly once, seals the
// epoch, and advances the checkpoint to End. Only after this returns nil is
// the end confirmed; any earlier failure leaves the previous checkpoint
// intact, so a restart repeats exactly this interval.
func (c *Cycle) Confirm() error {
	c.mu.Lock()
	if !c.open {
		c.mu.Unlock()
		return mkErr(KindResourceExhausted, "link %s: cycle already finished", c.link)
	}
	ws := c.dedup.Merged()
	c.mu.Unlock()

	if Position(len(ws)) != c.r.End-c.r.Start {
		return mkErr(KindResourceExhausted, "link %s: interval (%d,%d] needs %d writes, %d accepted",
			c.link, c.r.Start, c.r.End, c.r.End-c.r.Start, len(ws))
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].Seq < ws[j].Seq })
	for i, w := range ws {
		if want := c.r.Start + Position(i) + 1; w.Seq != want {
			return mkErr(KindStartMismatch, "link %s: coverage hole at seq %d", c.link, want)
		}
	}
	// Advance the checkpoint before sealing. This keeps "sealed epoch"
	// synonymous with "confirmed epoch", which SafeStart relies on: a failed
	// save leaves the epoch unsealed and untrusted. A crash between the save
	// and the seal only makes derivation return an earlier (still safe) start.
	if err := c.mgr.cp.Save(c.link, c.r.End); err != nil {
		return mkErr(KindResourceExhausted, "link %s: checkpoint save failed: %v", c.link, err)
	}
	if err := c.mgr.history.Seal(c.link, c.epoch, c.r.End); err != nil {
		return mkErr(KindResourceExhausted, "link %s: seal failed: %v", c.link, err)
	}

	c.mu.Lock()
	c.open = false
	c.mu.Unlock()
	c.mgr.finishCycle(c.link)
	return nil
}

// Abandon drops the unconfirmed cycle without touching the checkpoint. The
// next Begin on this link restarts from the still-confirmed start and must
// produce output identical to an uninterrupted run.
func (c *Cycle) Abandon() {
	c.mu.Lock()
	wasOpen := c.open
	c.open = false
	c.mu.Unlock()
	if wasOpen {
		c.mgr.finishCycle(c.link)
	}
}
