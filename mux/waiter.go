package mux

import "time"

// waiter tracks one in-flight registration. payload and err are written
// exactly once, before done is closed, so readers that observe done may
// read them without holding the lock.
type waiter struct {
	id       string
	ch       chan []byte
	done     chan struct{}
	deadline time.Time
	timed    bool
	payload  []byte
	err      error
}

func newWaiter(id string, deadline time.Time, timed bool) *waiter {
	return &waiter{
		id:       id,
		ch:       make(chan []byte, 1),
		done:     make(chan struct{}),
		deadline: deadline,
		timed:    timed,
	}
}

// finish completes the waiter exactly once. The caller must hold the Mux
// lock and must have already removed the waiter from the pending map.
func (w *waiter) finish(payload []byte, err error) {
	w.payload = payload
	w.err = err
	if payload != nil {
		w.ch <- payload
	}
	close(w.ch)
	close(w.done)
}
