// Package align aligns irregular samples to fixed-width buckets anchored at
// the absolute time origin and aggregates them under a sliding memory window.
package align

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/agg"
	"ontology/point"
)

var (
	// ErrInvalidStep is returned when step is not positive.
	ErrInvalidStep = errors.New("align: step must be positive")
	// ErrNaNValue is returned when a pushed point carries NaN.
	ErrNaNValue = errors.New("align: NaN value rejected")
	// ErrLateBeyondWindow is returned when a point is older than the
	// already-evicted watermark and cannot be placed.
	ErrLateBeyondWindow = errors.New("align: point later than window watermark")
)

// BucketStart returns floor(ts/step)*step using mathematical floor.
// Go integer division truncates toward zero, so negative remainders need a
// correction: ts=-1, step=10 must map to -10, not 0.
func BucketStart(ts, step int64) (int64, error) {
	if step <= 0 {
		return 0, ErrInvalidStep
	}
	q := ts / step
	if r := ts % step; r != 0 && (ts < 0) != (step < 0) {
		q--
	}
	return q * step, nil
}

// Contains reports whether ts falls in the left-closed/right-open bucket
// [start, start+step): start <= ts < start+step.
func Contains(start, step, ts int64) (bool, error) {
	if step <= 0 {
		return false, ErrInvalidStep
	}
	return ts >= start && ts < start+step, nil
}

type openBucket struct {
	start  int64
	points []sample
}

type sample struct {
	point.Point
}

// Stats exposes counters useful for proving resource bounds.
type Stats struct {
	PointsProcessed int64
	SkippedNaN      int64
	PeakResident    int
}

// Downsampler accumulates points and emits finalized buckets through the
// callback given at construction. At most maxBuckets buckets stay resident.
type Downsampler struct {
	mu         sync.Mutex
	step       int64
	maxBuckets int64
	buckets    map[int64]*openBucket
	lo, hi     int64
	haveRange  bool
	processed  int64
	skipped    int64
	peak       int
	emit       func(point.Bucket)
}

// New returns a Downsampler. maxBuckets must be positive.
func New(step, maxBuckets int64, emit func(point.Bucket)) (*Downsampler, error) {
	if step <= 0 {
		return nil, ErrInvalidStep
	}
	if maxBuckets <= 0 {
		return nil, errors.New("align: maxBuckets must be positive")
	}
	return &Downsampler{
		step:       step,
		maxBuckets: maxBuckets,
		buckets:    make(map[int64]*openBucket),
		emit:       emit,
	}, nil
}

// Push inserts one point, finalizing the oldest buckets if the window is full.
func (d *Downsampler) Push(p point.Point) error {
	if p.IsNaN() {
		d.mu.Lock()
		d.skipped++
		d.mu.Unlock()
		return ErrNaNValue
	}
	start, err := BucketStart(p.TS, d.step)
	if err != nil {
		return err
	}
	idx := start / d.step

	d.mu.Lock()
	if d.haveRange && idx < d.lo {
		d.mu.Unlock()
		return ErrLateBeyondWindow
	}
	newHi := idx
	if d.haveRange && d.hi > newHi {
		newHi = d.hi
	}
	var ready []*openBucket
	curLo := d.lo
	for d.haveRange && newHi-curLo >= d.maxBuckets {
		key := curLo * d.step
		if fb, ok := d.buckets[key]; ok {
			ready = append(ready, fb)
		}
		delete(d.buckets, key)
		curLo++
	}
	d.lo = curLo

	if !d.haveRange {
		d.lo, d.hi, d.haveRange = idx, idx, true
	}
	b := d.buckets[start]
	if b == nil {
		b = &openBucket{start: start}
		d.buckets[start] = b
		if len(d.buckets) > d.peak {
			d.peak = len(d.buckets)
		}
	}
	if idx > d.hi {
		d.hi = idx
	}
	b.points = append(b.points, sample{Point: p})
	d.processed++

	d.mu.Unlock()

	for _, fb := range ready {
		d.finalize(fb)
	}
	return nil
}

func (d *Downsampler) finalize(b *openBucket) {
	sort.SliceStable(b.points, func(i, j int) bool {
		if b.points[i].TS != b.points[j].TS {
			return b.points[i].TS < b.points[j].TS
		}
		return math.Float64bits(b.points[i].Value) < math.Float64bits(b.points[j].Value)
	})
	ordered := make([]point.Point, len(b.points))
	for i, s := range b.points {
		ordered[i] = s.Point
	}
	r, err := agg.Aggregate(ordered)
	if err != nil {
		return
	}
	d.emit(point.Bucket{Start: b.start, Value: r.Mean, Count: r.Count})
}

// Drain finalizes and emits every resident bucket in ascending start order.
func (d *Downsampler) Drain() {
	d.mu.Lock()
	starts := make([]int64, 0, len(d.buckets))
	for s := range d.buckets {
		starts = append(starts, s)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
	pending := make([]*openBucket, len(starts))
	for i, s := range starts {
		pending[i] = d.buckets[s]
	}
	d.buckets = make(map[int64]*openBucket)
	d.haveRange = false
	d.mu.Unlock()

	for _, b := range pending {
		d.finalize(b)
	}
}

// Stats returns a snapshot of the internal counters.
func (d *Downsampler) Stats() Stats {
	d.mu.Lock()
	defer d.mu.Unlock()
	return Stats{
		PointsProcessed: d.processed,
		SkippedNaN:      d.skipped,
		PeakResident:    d.peak,
	}
}
