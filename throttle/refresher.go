// Package throttle implements key-scoped, logical-clock driven coalescing for
// materialized view refreshes.
package throttle

import (
	"errors"
	"sort"
	"sync"
	"time"
)

var (
	ErrNonPositiveParameter = errors.New("non-positive parameter")
	ErrEmptyKey             = errors.New("empty key")
	ErrTimeBeforeLast       = errors.New("timestamp before last successful operation")
	ErrTooManyPendingKeys   = errors.New("too many pending keys")
)

type RefreshRecord struct {
	Key         string
	Value       string
	Count       int
	ScheduledAt time.Time
	RefreshAt   time.Time
}

type Snapshot struct {
	View             map[string]string
	PendingKeyCount  int
	TotalBatchCount  int
	LastLogicalTime  time.Time
	RefreshedRecords []RefreshRecord
}

type pendingBatchData struct {
	value     string
	count     int
	refreshAt time.Time
}

type Refresher struct {
	mu               sync.Mutex
	interval         time.Duration
	maxPendingKeys   int
	pending          map[string]pendingBatchData
	view             map[string]string
	totalBatches     int
	lastLogicalTime  time.Time
	refreshedRecords []RefreshRecord
	closed           bool
}

// New creates a refresher with a positive per-key debounce interval and a
// positive limit on distinct keys that may wait for refresh.
func New(interval time.Duration, maxPendingKeys int) (*Refresher, error) {
	if interval <= 0 || maxPendingKeys <= 0 {
		return nil, ErrNonPositiveParameter
	}

	return &Refresher{
		interval:       interval,
		maxPendingKeys: maxPendingKeys,
		pending:        make(map[string]pendingBatchData),
		view:           make(map[string]string),
	}, nil
}

// Change records one value change for a key. A pending batch for the same key
// accumulates count, takes the latest value, and postpones its scheduled time.
func (r *Refresher) Change(key, value string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateTime(at); err != nil {
		return err
	}
	if key == "" {
		return ErrEmptyKey
	}
	if r.closed {
		return ErrRefresherClosed
	}

	batch, exists := r.pending[key]
	if !exists {
		if len(r.pending) >= r.maxPendingKeys {
			return ErrTooManyPendingKeys
		}
	}
	batch.value = value
	batch.count++
	batch.refreshAt = at.Add(r.interval)
	r.pending[key] = batch
	r.lastLogicalTime = at
	return nil
}

// Advance applies every pending batch whose scheduled time is not after now.
func (r *Refresher) Advance(now time.Time) ([]RefreshRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateTime(now); err != nil {
		return nil, err
	}
	r.lastLogicalTime = now

	return r.refreshDue(now, false), nil
}

// Shutdown immediately applies all pending batches and rejects later changes.
func (r *Refresher) Shutdown(at time.Time) ([]RefreshRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.validateTime(at); err != nil {
		return nil, err
	}
	r.lastLogicalTime = at
	r.closed = true

	return r.refreshDue(at, true), nil
}

// Snapshot returns a point-in-time, deeply copied read model.
func (r *Refresher) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()

	records := make([]RefreshRecord, len(r.refreshedRecords))
	copy(records, r.refreshedRecords)

	return Snapshot{
		View:             cloneStringMap(r.view),
		PendingKeyCount:  len(r.pending),
		TotalBatchCount:  r.totalBatches,
		LastLogicalTime:  r.lastLogicalTime,
		RefreshedRecords: records,
	}
}

func (r *Refresher) View() map[string]string {
	return r.Snapshot().View
}

func (r *Refresher) TotalBatchCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.totalBatches
}

func (r *Refresher) PendingKeyCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

func (r *Refresher) LastLogicalTime() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastLogicalTime
}

var ErrRefresherClosed = errors.New("refresher is closed")

func (r *Refresher) validateTime(at time.Time) error {
	if at.Before(r.lastLogicalTime) {
		return ErrTimeBeforeLast
	}
	return nil
}

func (r *Refresher) refreshDue(now time.Time, all bool) []RefreshRecord {
	keys := make([]string, 0, len(r.pending))
	for key, batch := range r.pending {
		if all || !batch.refreshAt.After(now) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	records := make([]RefreshRecord, 0, len(keys))
	for _, key := range keys {
		batch := r.pending[key]
		record := RefreshRecord{
			Key:         key,
			Value:       batch.value,
			Count:       batch.count,
			ScheduledAt: batch.refreshAt,
			RefreshAt:   now,
		}
		r.view[key] = batch.value
		records = append(records, record)
		r.refreshedRecords = append(r.refreshedRecords, record)
		delete(r.pending, key)
		r.totalBatches++
	}
	return records
}

func cloneStringMap(input map[string]string) map[string]string {
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
