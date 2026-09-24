// Package req defines write requests and their per-caller results.
package req

// Request is a single append request submitted by a caller.
type Request struct {
	// Payload is the raw bytes to persist. Empty is legal.
	Payload []byte
	// result is the caller-private channel on which the leader reports
	// exactly one outcome. A private channel prevents result mix-up
	// between concurrent callers sharing one batch.
	result chan Result
}

// New creates a request carrying a private, unbuffered result channel.
func New(payload []byte) *Request {
	return &Request{Payload: payload, result: make(chan Result, 1)}
}

// Result reports the outcome of one request.
type Result struct {
	// Seq is the global sequence number assigned to the request.
	// It is zero unless Err is nil.
	Seq uint64
	// Err is the batch-level failure, or nil on success.
	Err error
}

// ResultChan exposes the private outcome channel to the group-commit leader.
func (r *Request) ResultChan() chan<- Result { return r.result }

// Wait blocks until the leader reports the outcome.
func (r *Request) Wait() Result { return <-r.result }
