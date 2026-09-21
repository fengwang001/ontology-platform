package batcher

// Add buffers item, sealing and delivering a batch synchronously when
// maxItems or maxBytes is reached. A delivery failure is recorded in
// Failed; Add itself only reports ErrTooLarge and ErrClosed.
func (b *Batcher) Add(item string) error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	if len(item) > b.maxBytes {
		b.mu.Unlock()
		return ErrTooLarge
	}
	if len(b.items) == 0 {
		b.firstAt = b.now()
	}
	b.items = append(b.items, item)
	b.bytes += len(item)
	full := len(b.items) >= b.maxItems || b.bytes >= b.maxBytes
	b.mu.Unlock()
	if full {
		_ = b.drain()
	}
	return nil
}

// Tick seals the current buffer if it has aged past maxAge, measured
// from the first buffered item. An empty buffer is a no-op.
func (b *Batcher) Tick() {
	b.mu.Lock()
	aged := len(b.items) > 0 && !b.closed && b.now().Sub(b.firstAt) >= b.maxAge
	b.mu.Unlock()
	if aged {
		_ = b.drain()
	}
}

// Flush seals and delivers the current buffer. An empty buffer is a
// no-op and does not consume a sequence number.
func (b *Batcher) Flush() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return ErrClosed
	}
	b.mu.Unlock()
	return b.drain()
}

// drain seals and delivers batches until the buffer is empty.
func (b *Batcher) drain() error {
	for {
		batch, ok := b.seal()
		if !ok {
			return nil
		}
		if err := b.deliver(batch); err != nil {
			return err
		}
	}
}

// Close flushes and closes the Batcher. It is idempotent: later calls
// do nothing and return nil.
func (b *Batcher) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	b.mu.Unlock()
	return b.drain()
}

// Pending reports the buffered item count and byte total.
func (b *Batcher) Pending() (items, bytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.items), b.bytes
}

// Failed returns the failed batches in failure order.
func (b *Batcher) Failed() []Batch {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Batch, len(b.failed))
	copy(out, b.failed)
	return out
}

// seal cuts the pending buffer into the next numbered batch.
func (b *Batcher) seal() (Batch, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.items) == 0 {
		return Batch{}, false
	}
	b.seq++
	batch := Batch{Seq: b.seq, Items: b.items, Bytes: b.bytes}
	b.items = nil
	b.bytes = 0
	return batch, true
}

// deliver hands the batch to the sink without holding the lock, so
// the sink may safely call back into the Batcher. A failed batch is
// recorded in Failed and never returns to the buffer.
func (b *Batcher) deliver(batch Batch) error {
	err := b.sink.Deliver(batch)
	if err == nil {
		return nil
	}
	b.mu.Lock()
	b.failed = append(b.failed, batch)
	b.mu.Unlock()
	return err
}
