package batcher

import "time"

// Add buffers item, sealing and delivering a batch synchronously when a
// count or byte trigger is reached. A single item longer than maxBytes
// is rejected with ErrTooLarge and never enters the buffer.
func (b *Batcher) Add(item string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	if len(item) > b.maxBytes {
		return ErrTooLarge
	}
	if len(b.items) == 0 {
		b.since = b.now()
	}
	b.items = append(b.items, item)
	b.bytes += len(item)
	if len(b.items) >= b.maxItems || b.bytes >= b.maxBytes {
		return b.flushLocked()
	}
	return nil
}

// Tick seals the current batch if it has aged past maxAge, measured from
// the first buffered item according to the injected clock. An empty
// buffer never produces a batch.
func (b *Batcher) Tick() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || len(b.items) == 0 {
		return
	}
	if !b.now().Before(b.since.Add(b.maxAge)) {
		_ = b.flushLocked()
	}
}

// Flush seals and delivers the current buffer immediately. It is a no-op
// on an empty buffer and returns ErrClosed after Close.
func (b *Batcher) Flush() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrClosed
	}
	return b.flushLocked()
}

// Close flushes any pending buffer and shuts the batcher down. It is
// idempotent: later calls are no-ops and never re-deliver.
func (b *Batcher) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	err := b.flushLocked()
	b.closed = true
	return err
}

// Pending reports the item count and byte size of the current buffer.
func (b *Batcher) Pending() (items, bytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items), b.bytes
}

// Failed returns the batches whose delivery failed, in failure order.
func (b *Batcher) Failed() []Batch {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Batch, len(b.failed))
	copy(out, b.failed)
	return out
}

// flushLocked seals the current buffer into the next sequence number and
// delivers it. The buffer is detached before Deliver runs, so during
// delivery the batch is neither pending nor failed. The mutex is
// released around Deliver so sinks may call back into the batcher.
func (b *Batcher) flushLocked() error {
	if len(b.items) == 0 {
		return nil
	}
	b.seq++
	batch := Batch{Seq: b.seq, Items: b.items, Bytes: b.bytes}
	b.items = nil
	b.bytes = 0
	b.since = time.Time{}

	b.mu.Unlock()
	b.deliver.Lock()
	err := b.sink.Deliver(batch)
	b.deliver.Unlock()
	b.mu.Lock()

	if err != nil {
		b.failed = append(b.failed, batch)
	}
	return err
}
