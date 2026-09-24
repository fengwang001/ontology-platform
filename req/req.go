// Package req defines a single write request and its per-caller result.
package req

// Request is one write submitted by one caller. Each request owns a private
// result channel so waiters can never receive another caller's result.
type Request struct {
	Data    []byte
	result  chan Result
	prepared bool
}

// Result reports the global sequence number assigned to a request, or the
// error that made its whole batch fail.
type Result struct {
	Seq uint64
	Err error
}

// New creates a request carrying data and a private buffered result channel.
func New(data []byte) *Request {
	return &Request{Data: data, result: make(chan Result, 1), prepared: true}
}

// Done returns the channel on which exactly one result is delivered.
func (r *Request) Done() <-chan Result {
	return r.result
}

// Wait blocks until the single result is available.
func (r *Request) Wait() Result {
	return <-r.result
}

// Resolve delivers exactly one result; the one-element buffer keeps the
// coordinator from blocking on slow callers.
func (r *Request) Resolve(res Result) {
	if !r.prepared {
		r.result = make(chan Result, 1)
		r.prepared = true
	}
	select {
	case r.result <- res:
	default:
	}
}
