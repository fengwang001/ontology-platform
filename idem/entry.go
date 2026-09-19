package idem

import "time"

// entry is a single idempotency key's slot. It is created in the in-flight
// state and either becomes a completed record or is removed from the executor
// when the result must not be cached.
//
// Fields written by the leader goroutine before done is closed are safely
// observed by waiters after they receive from done: the channel close is the
// synchronization edge.
type entry struct {
	fp   string
	done chan struct{}

	res         Result
	err         error
	retriable   bool
	completedAt time.Time
}

func newEntry(fp string) *entry {
	return &entry{
		fp:   fp,
		done: make(chan struct{}),
	}
}

// inFlight reports whether the leader has not finished yet. Selecting on the
// done channel never blocks once it is closed, so this is a non-blocking
// readiness probe.
func (en *entry) inFlight() bool {
	select {
	case <-en.done:
		return false
	default:
		return true
	}
}

// run executes fn as the leader, capturing panics so they never crash the
// process. It returns whether the produced result must not be cached.
func (en *entry) run(fn func() (Result, error), now func() time.Time) (notCached bool) {
	defer close(en.done)

	defer func() {
		if p := recover(); p != nil {
			en.res = Result{}
			en.err = panicError(p)
			en.retriable = true
			notCached = true
		}
	}()

	res, err := fn()
	en.res, en.err = res, err
	en.retriable = isRetriable(err)
	en.completedAt = now()
	return en.retriable
}

// expired reports whether the completed record has expired under clock now.
// Records never expire when ttl <= 0.
func (en *entry) expired(now func() time.Time, ttl time.Duration) bool {
	if ttl <= 0 {
		return false
	}
	return !now().Before(en.completedAt.Add(ttl))
}
