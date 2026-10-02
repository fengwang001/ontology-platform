package ontology

import (
	"errors"
	"sync"
)

var ErrInvalidArgument = errors.New("ontology: invalid argument")

type Row struct {
	Key []byte
	TS  int64
	Val int64
}

type Output struct {
	Key []byte
	TS  int64
	Val int64
	Sum int64
	Cnt int64
	Max int64
}

type RangeAggregator struct {
	mu              sync.RWMutex
	width           int64
	allowedLateness int64
	capacity        int64
	wm              int64
	seq             int64
	nextRowID       int64
	retainedCount   int64
	lateDropped     int64
	supplemented    int64
	released        int64
	frameWork       int64
	lateWork        int64
	buffer          *bufferHeap
	keys            map[string]*keyAggregate
	oldestBuckets   *oldBucketHeap
}

func NewRangeAggregator(width int64, allowedLateness int64, capacity int64) (*RangeAggregator, error) {
	const maxBound = int64(1_000_000_000_000_000)
	if width < 0 || width > maxBound ||
		allowedLateness < 0 || allowedLateness > maxBound ||
		capacity < 1 || capacity > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	return &RangeAggregator{
		width:           width,
		allowedLateness: allowedLateness,
		capacity:        capacity,
		wm:              -1,
		buffer:          &bufferHeap{},
		keys:            make(map[string]*keyAggregate),
		oldestBuckets:   &oldBucketHeap{},
	}, nil
}

func (a *RangeAggregator) Insert(row Row) (Output, string, error) {
	if len(row.Key) == 0 || row.TS < 0 || row.TS > 1_000_000_000_000_000 ||
		row.Val < -1_000_000_000 || row.Val > 1_000_000_000 {
		return Output{}, "rejected", ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.insertLocked(row)
}

func (a *RangeAggregator) Advance(watermark int64) ([]Output, error) {
	if watermark < 0 || watermark > 1_000_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if watermark < a.wm {
		return nil, ErrInvalidArgument
	}
	return a.advanceLocked(watermark), nil
}

func (a *RangeAggregator) Retained() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.retainedCount
}

func (a *RangeAggregator) LateDropped() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lateDropped
}

func (a *RangeAggregator) Supplemented() int64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.supplemented
}
