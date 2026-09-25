// Package api is the in-process entry point for ingest, compaction and
// live-value inspection. It depends on comp; the reverse is impossible.
package api

import (
	"errors"
	"fmt"
	"reflect"
	"sync"

	"ontology/comp"
	"ontology/seg"
)

// ErrBadVersion: non-positive or non-increasing version. The other
// three failure causes come from seg (empty key, bad op, bad val).
var (
	ErrBadVersion = errors.New("api: version must be positive and strictly increasing")
	ErrSelfCheck  = errors.New("api: self check failed")
)

func putR(k string, v int64, val string) seg.Rec {
	return seg.Rec{Key: k, Version: v, Op: seg.OpPut, Val: val}
}
func delR(k string, v int64) seg.Rec { return seg.Rec{Key: k, Version: v, Op: seg.OpDel} }

// DB is an in-memory MVCC KV store. State sits behind one RWMutex:
// View/Watermark take RLock and run concurrently; writes take Lock.
type DB struct {
	mu     sync.RWMutex
	maxVer int64
	latest map[string]seg.Rec // one pointer per key to its newest record
	mg     *comp.Merger
}

// New returns an empty DB.
func New() *DB {
	return &DB{latest: map[string]seg.Rec{}, mg: comp.NewMerger()}
}

// Ingest appends a record and advances the global max version. All
// validation precedes any mutation, so rejection leaves no trace.
func (d *DB) Ingest(r seg.Rec) error {
	if err := r.Valid(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Version <= 0 || !seg.Newer(r.Version, d.maxVer) {
		return ErrBadVersion
	}
	d.latest[r.Key] = r
	d.maxVer = r.Version
	return nil
}

// Compact merges the segments (any order) and raises the watermark. A
// rejected merge changes nothing.
func (d *DB) Compact(segs []*comp.Segment) (*comp.Segment, error) {
	out, err := d.mg.Compact(segs)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if out.Watermark > d.maxVer {
		d.maxVer = out.Watermark
	}
	d.mu.Unlock()
	return out, nil
}

// View returns a fresh map of live values; tombstoned keys are absent.
func (d *DB) View() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	v := make(map[string]string, len(d.latest))
	for k, r := range d.latest {
		if !r.IsTombstone() {
			v[k] = r.Val
		}
	}
	return v
}

// Watermark is the largest version ever seen; it only moves up.
func (d *DB) Watermark() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.maxVer
}

// batchView is the reference: max-version record per key, dropping keys
// whose winner is a tombstone.
func batchView(recs []seg.Rec) map[string]string {
	win := map[string]seg.Rec{}
	for _, r := range recs {
		if w, ok := win[r.Key]; !ok || r.Version > w.Version {
			win[r.Key] = r
		}
	}
	out := map[string]string{}
	for k, r := range win {
		if !r.IsTombstone() {
			out[k] = r.Val
		}
	}
	return out
}

// SelfCheck verifies the four invariants on the section-3 sequence, on
// a fresh internal DB, so the receiver is untouched and concurrent-safe.
func (d *DB) SelfCheck() error {
	t := New()
	recs := []seg.Rec{
		putR("a", 1, "x"), putR("b", 2, "y"), putR("a", 3, "x2"), putR("c", 4, "z"),
		delR("b", 5), delR("a", 6), putR("c", 7, "z2"), putR("b", 8, "y2"),
	}
	for _, r := range recs {
		if err := t.Ingest(r); err != nil {
			return fmt.Errorf("%w: ingest v%d: %v", ErrSelfCheck, r.Version, err)
		}
	}
	if !reflect.DeepEqual(t.View(), batchView(recs)) { // invariant 1
		return fmt.Errorf("%w: view != batch recompute", ErrSelfCheck)
	}
	s1, s2 := &comp.Segment{Records: recs[:5]}, &comp.Segment{Records: recs[5:]}
	out, err := t.Compact([]*comp.Segment{s2, s1}) // [seg2, seg1] on purpose
	if err != nil {
		return fmt.Errorf("%w: compact: %v", ErrSelfCheck, err)
	}
	if !reflect.DeepEqual(out.Records, []seg.Rec{putR("b", 8, "y2"), putR("c", 7, "z2")}) || out.Watermark != 8 { // invariants 2,3
		return fmt.Errorf("%w: segment=%+v wm=%d", ErrSelfCheck, out.Records, out.Watermark)
	}
	bad := []seg.Rec{
		putR("", 9, "q"), putR("a", 3, "q"),
		{Key: "d", Version: 9, Op: "patch", Val: "q"}, {Key: "d", Version: 9, Op: seg.OpDel, Val: "q"},
	}
	for _, r := range bad {
		if err := t.Ingest(r); err == nil { // invariant 4: each must be rejected
			return fmt.Errorf("%w: accepted bad record %+v", ErrSelfCheck, r)
		}
	}
	if t.Watermark() != 8 || !reflect.DeepEqual(t.View(), batchView(recs)) {
		return fmt.Errorf("%w: state changed after rejection", ErrSelfCheck)
	}
	if err := t.Ingest(putR("d", 9, "q")); err != nil {
		return fmt.Errorf("%w: unusable after rejection: %v", ErrSelfCheck, err)
	}
	return nil
}
