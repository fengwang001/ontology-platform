package groupcommit

import (
	"context"
	"fmt"
)

// run is the single worker goroutine. Being the only goroutine that builds
// batches, assigns sequence numbers and calls Persist enforces FIFO ordering
// and the "at most one batch persisting at a time" invariant.
func (gc *GroupCommitter) run() {
	defer close(gc.done)

	var (
		nextSeq int64 = 1 // next durable sequence number to assign
		batchNo int64
		queue   []*pending
	)

	deliver := func(batch []*pending, firstSeq int64, no int64, persistErr error) {
		for i, req := range batch {
			res := batchResult{seq: firstSeq + int64(i), batch: no}
			if persistErr != nil {
				res.failed = true
				res.err = fmt.Errorf("%w: batch=%d: %v", ErrBatchFailed, no, persistErr)
			}
			// Buffered channel: never blocks even if the caller gave up.
			req.result <- res
		}
	}

	for {
		// Block until at least one request arrives or shutdown starts.
		select {
		case req := <-gc.submit:
			queue = append(queue, req)
		case <-gc.shutdown:
			// Drain everything already admitted (including entries sitting in
			// the submit channel buffer), then exit. No new send can succeed
			// once shutdown is closed, so a non-blocking drain is complete.
			for {
				select {
				case req := <-gc.submit:
					queue = append(queue, req)
				default:
					goto shutdownDrain
				}
			}
		shutdownDrain:
			for len(queue) > 0 {
				batch, rest := gc.takeBatch(queue)
				queue = rest
				batchNo++
				firstSeq := nextSeq
				nextSeq, _ = gc.persistBatch(batch, firstSeq, batchNo, deliver)
			}
			return
		}

		// Test-only gate: allow preloading before the first cut.
		if gc.batchGate != nil {
			<-gc.batchGate
		}

		// Drain everything currently buffered so a batch takes as much work
		// as its count/byte limits allow.
		draining := true
		for draining {
			select {
			case req := <-gc.submit:
				queue = append(queue, req)
			default:
				draining = false
			}
		}

		for len(queue) > 0 {
			batch, rest := gc.takeBatch(queue)
			queue = rest
			batchNo++
			firstSeq := nextSeq
			advanced, _ := gc.persistBatch(batch, firstSeq, batchNo, deliver)
			nextSeq = advanced

			// Pick up requests that arrived while the batch was persisting.
			for {
				select {
				case req := <-gc.submit:
					queue = append(queue, req)
				default:
					goto drained
				}
			}
		drained:
		}
	}
}

// takeBatch splits off one batch from the FIFO queue head.
//
// It takes entries until the count limit is reached or adding the next entry
// would make the batch exceed the byte limit. Sequence numbers are assigned
// later, immediately before persistence, in exactly this order.
func (gc *GroupCommitter) takeBatch(queue []*pending) (batch, rest []*pending) {
	totalBytes := 0
	for i, req := range queue {
		size := len(req.payload)
		if i >= gc.maxEntries || totalBytes+size > gc.maxBytes {
			return queue[:i], queue[i:]
		}
		totalBytes += size
	}
	return queue, nil
}

// persistBatch assigns a contiguous run of sequence numbers, persists the
// batch atomically and delivers one result per entry. It returns the next
// sequence number to use: on failure the assigned run is recycled in full,
// so durable sequence numbers stay contiguous starting at 1 with no gaps or
// duplicates.
func (gc *GroupCommitter) persistBatch(
	batch []*pending,
	firstSeq int64,
	batchNo int64,
	deliver func(batch []*pending, firstSeq int64, no int64, persistErr error),
) (int64, error) {
	payloads := make([][]byte, len(batch))
	totalBytes := 0
	for i, req := range batch {
		payloads[i] = req.payload
		totalBytes += len(req.payload)
	}

	// Sequence numbers are assigned now, after batching is final and before
	// persistence hands the batch to storage.
	gc.log.Printf(
		"batch start no=%d entries=%d bytes=%d seq=%d..%d decision=count-or-byte-limit",
		batchNo, len(batch), totalBytes, firstSeq, firstSeq+int64(len(batch))-1,
	)

	err := gc.persister.Persist(context.Background(), payloads)
	if err != nil {
		gc.log.Printf(
			"batch fail no=%d entries=%d bytes=%d seq=%d..%d decision=rollback-recycle reason=%v",
			batchNo, len(batch), totalBytes, firstSeq, firstSeq+int64(len(batch))-1, err,
		)
		deliver(batch, firstSeq, batchNo, err)
		return firstSeq, nil // recycle: the run was never durable
	}

	gc.log.Printf(
		"batch commit no=%d entries=%d bytes=%d seq=%d..%d decision=advance-seq",
		batchNo, len(batch), totalBytes, firstSeq, firstSeq+int64(len(batch))-1,
	)
	deliver(batch, firstSeq, batchNo, nil)
	return firstSeq + int64(len(batch)), nil
}

// Close stops accepting new requests, waits for every request already in the
// queue to receive its result, and then returns.
func (gc *GroupCommitter) Close() error {
	select {
	case <-gc.shutdown:
		// Already closing/closed: just wait for the drain to finish.
	default:
		close(gc.shutdown)
	}
	<-gc.done
	return nil
}
