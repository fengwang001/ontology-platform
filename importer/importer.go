// Package importer orchestrates batch import in four phases
// (Prepare -> Write -> Verify -> Commit) with idempotent, resumable writes.
package importer

import (
	"errors"
	"fmt"
	"time"

	"ontology/batch"
	"ontology/progress"
	"ontology/store"
)

// ErrAlreadyCommitted is returned by Run when the batch finished earlier;
// it signals idempotent success, not a failure.
var ErrAlreadyCommitted = errors.New("importer: batch already committed")

// ErrBusy is returned when another importer holds the batch lock.
var ErrBusy = progress.ErrBusy

// Importer imports batches into a store, persisting progress under dir.
type Importer struct {
	st   *store.Store
	dir  string
	gran int // progress flush granularity N (records per chunk)
	now  func() time.Time
	ttl  time.Duration

	resumeRewrites int64 // records rewritten during resume (already in store)
	inflight       int64 // records currently held in the write pipeline
	peakInflight   int64 // historical maximum of inflight
}

// New creates an importer flushing progress every gran records.
func New(st *store.Store, dir string, gran int) *Importer {
	if gran <= 0 {
		gran = 1
	}
	return &Importer{st: st, dir: dir, gran: gran, now: time.Now, ttl: time.Minute}
}

// ResumeRewrites reports how many records were rewritten while resuming.
func (im *Importer) ResumeRewrites() int64 { return im.resumeRewrites }

// PeakInflight reports the historical peak of records held in memory.
func (im *Importer) PeakInflight() int64 { return im.peakInflight }

// Run executes Prepare -> Write -> Verify -> Commit for b.
func (im *Importer) Run(b *batch.Batch) error {
	// Prepare: idempotency check, lock, progress recovery, resume point.
	if progress.Committed(im.dir, b.ID) {
		return ErrAlreadyCommitted
	}
	release, err := progress.Acquire(im.dir, b.ID, "importer", im.now(), im.ttl)
	if err != nil {
		return err
	}
	defer release()
	p, perr := progress.Load(im.dir, b.ID)
	if perr != nil && !errors.Is(perr, progress.ErrHeader) &&
		!errors.Is(perr, progress.ErrInterval) && !errors.Is(perr, progress.ErrCRC) {
		return perr
	}
	p.BatchID, p.Total = b.ID, b.Len()
	frontier := min(p.Frontier(), im.storeFrontier(b))

	// Write: chunk-wise idempotent puts, flushing progress per chunk.
	for i := frontier; i < b.Len(); {
		end := min(i+im.gran, b.Len())
		im.enter(end - i)
		failed := false
		for _, k := range b.Keys[i:end] {
			existed, err := im.st.Put(k, batch.RecordValue(b.ID, k))
			if err != nil {
				failed = true
				break
			}
			if existed {
				im.resumeRewrites++
			}
		}
		im.exit(end - i)
		if failed {
			return errors.New("importer: write failed, progress kept at last flush")
		}
		p.Intervals = append(p.Intervals, [2]int{i, end})
		if err := progress.Save(im.dir, p); err != nil {
			return err
		}
		i = end
	}

	// Verify: full linear check that every record is present.
	for i := 0; i < b.Len(); i++ {
		if v, ok := im.st.Get(b.Keys[i]); !ok || v != batch.RecordValue(b.ID, b.Keys[i]) {
			return fmt.Errorf("importer: verify failed at record %d", i)
		}
	}

	// Commit: durable marker; the lock is released by the deferred call.
	return progress.MarkCommitted(im.dir, b.ID)
}

// storeFrontier binary-searches the prefix of manifest records present in
// the store: O(log n) probes, trusting storage over the progress file.
func (im *Importer) storeFrontier(b *batch.Batch) int {
	lo, hi := 0, b.Len()
	for lo < hi {
		mid := (lo + hi + 1) / 2
		v, ok := im.st.Get(b.Keys[mid-1])
		if ok && v == batch.RecordValue(b.ID, b.Keys[mid-1]) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

func (im *Importer) enter(n int) {
	im.inflight += int64(n)
	if im.inflight > im.peakInflight {
		im.peakInflight = im.inflight
	}
}

func (im *Importer) exit(n int) { im.inflight -= int64(n) }
