// Package req defines the write request and per-caller result shared by the
// batcher, commit orchestrator and recovery code.
package req

// Request is a single write submitted by a caller.
type Request struct {
	// Payload is the opaque byte string to persist. Empty is valid.
	Payload []byte
	// result carries exactly one outcome back to the submitting goroutine.
	// Cap 1 so the leader never blocks on a slow waiter.
	result chan Result
}

// New builds a request with its private reply channel.
func New(payload []byte) *Request {
	return &Request{Payload: payload, result: make(chan Result, 1)}
}

// Result is what a caller receives after its batch is decided.
type Result struct {
	// Seq is the global sequence number, 1-based; 0 when Err != nil.
	Seq uint64
	// Err is the batch-level failure, nil on success.
	Err error
}

// Done returns the channel on which the caller waits for its own outcome.
func (r *Request) Done() <-chan Result { return r.result }

// Resolve delivers exactly one outcome. Called by the batch leader only.
func (r *Request) Resolve(res Result) { r.result <- res }
