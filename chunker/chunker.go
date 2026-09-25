package chunker

import "time"

// Policy controls chunk boundaries.
type Policy struct {
	Min    int
	Max    int
	Window time.Duration
}

// Chunk is a sealed data chunk.
type Chunk struct {
	Data []byte
}

// Len returns the chunk data length.
func (c Chunk) Len() int { return len(c.Data) }

// Chunker turns a byte stream into chunks. Sealing depends only on the byte
// stream and the injected clock, never on upstream Write call boundaries.
type Chunker struct {
	pol         Policy
	pend        []byte
	start       time.Time
	open        bool
	sealed      []Chunk
	sealedBytes int
}

// New creates a chunker. Non-positive Min is treated as 1.
func New(p Policy) *Chunker {
	if p.Min < 1 {
		p.Min = 1
	}
	return &Chunker{pol: p}
}

// Feed appends bytes. A zero-length feed is a complete no-op: it must not
// start a time window, because a size-0 chunk is the stream terminator.
func (c *Chunker) Feed(b []byte, now time.Time) {
	if len(b) == 0 {
		return
	}
	if !c.open {
		c.open = true
		c.start = now
	}
	c.pend = append(c.pend, b...)
	c.seal(now, false)
}

// Tick re-evaluates the time window without adding bytes.
func (c *Chunker) Tick(now time.Time) { c.seal(now, false) }

// Flush seals every pending byte (used by Close).
func (c *Chunker) Flush(now time.Time) { c.seal(now, true) }

func (c *Chunker) seal(now time.Time, flush bool) {
	for len(c.pend) >= c.pol.Max {
		c.emit(c.pol.Max)
	}
	if len(c.pend) == 0 {
		c.open = false
		return
	}
	if flush {
		c.emit(len(c.pend))
		c.open = false
		return
	}
	if len(c.pend) >= c.pol.Min && !now.Before(c.start.Add(c.pol.Window)) {
		c.emit(len(c.pend))
		c.open = false
	}
}

func (c *Chunker) emit(n int) {
	data := make([]byte, n)
	copy(data, c.pend[:n])
	c.sealed = append(c.sealed, Chunk{Data: data})
	c.sealedBytes += n
	m := copy(c.pend, c.pend[n:])
	c.pend = c.pend[:m]
}

// Next peeks at the oldest sealed chunk without removing it.
func (c *Chunker) Next() (Chunk, bool) {
	if len(c.sealed) == 0 {
		return Chunk{}, false
	}
	return c.sealed[0], true
}

// Pop removes the oldest sealed chunk.
func (c *Chunker) Pop() {
	if len(c.sealed) == 0 {
		return
	}
	c.sealedBytes -= len(c.sealed[0].Data)
	c.sealed[0] = Chunk{}
	c.sealed = c.sealed[1:]
	if len(c.sealed) == 0 {
		c.sealed = nil
	}
}

// Pending returns unproduced source bytes (unsealed plus sealed-not-popped).
func (c *Chunker) Pending() int { return len(c.pend) + c.sealedBytes }

// Snapshot captures unsealed bytes and the group start.
func (c *Chunker) Snapshot() (pend []byte, start time.Time, open bool) {
	if len(c.pend) > 0 {
		pend = append([]byte(nil), c.pend...)
	}
	return pend, c.start, c.open
}

// Restore replaces the unsealed state (sealed queue must be empty).
func (c *Chunker) Restore(pend []byte, start time.Time, open bool) {
	c.pend = append(c.pend[:0], pend...)
	c.start = start
	c.open = open
}
