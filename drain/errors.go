package drain

import "errors"

// ErrShuttingDown is returned by Enter once shutdown has started.
var ErrShuttingDown = errors.New("drain: shutting down")

// ErrDrainTimeout is returned by Shutdown when the deadline is reached
// while requests are still in flight.
var ErrDrainTimeout = errors.New("drain: shutdown deadline exceeded")
