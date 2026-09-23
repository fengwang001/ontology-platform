package shard

import (
	"context"
	"time"
)

// Fake is a scripted shard used to inject faults without a network.
type Fake struct {
	// Records is the canonical valid batch.
	Records []Record
	Bound   int64

	// Delay is the wait before delivering each batch.
	Delay time.Duration
	// Timeout makes the shard block until ctx is done, delivering nothing.
	Timeout bool
	// Fail makes the shard deliver a terminal ErrFailed instead of data.
	Fail bool
	// Corrupt makes the delivered count disagree with the claim.
	Corrupt bool
	// Duplicate makes the valid batch be delivered a second time after Delay.
	Duplicate bool
}

// NewFake builds a healthy fake shard advertising bound for its records.
func NewFake(records []Record, bound int64) *Fake {
	return &Fake{Records: records, Bound: bound}
}

// Batch constructs one valid event for records, asserting bound as upper bound.
func Batch(records []Record, bound int64) Event {
	return Event{Records: records, Claimed: len(records), Bound: bound, BoundKnown: true}
}

// Fetch implements Shard.
func (f *Fake) Fetch(ctx context.Context, _ string) <-chan Event {
	out := make(chan Event, 2)
	go func() {
		defer close(out)
		if f.Timeout {
			<-ctx.Done()
			return
		}
		if !sleep(ctx, f.Delay) {
			return
		}
		if f.Fail {
			select {
			case out <- Event{Err: ErrFailed}:
			case <-ctx.Done():
			}
			return
		}
		ev := Batch(f.Records, f.Bound)
		if f.Corrupt {
			ev.Claimed = len(f.Records) + 1
		}
		if !emit(ctx, out, ev) {
			return
		}
		if f.Duplicate {
			if !sleep(ctx, f.Delay) {
				return
			}
			emit(ctx, out, Batch(f.Records, f.Bound))
		}
	}()
	return out
}

func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func emit(ctx context.Context, out chan<- Event, ev Event) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
