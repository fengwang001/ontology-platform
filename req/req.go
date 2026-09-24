// Package req defines write requests and their per-caller results.
package req

// Request is a single write submitted to the group committer.
// Each request carries its own reply channel so the leader can wake exactly
// the submitting goroutine.
type Request struct {
	Payload []byte
	reply   chan Result
}

// Result is delivered to the caller after its batch lands (or fails).
type Result struct {
	Seq int64 // global sequence number, -1 on error
	Err error
}

// New builds a request with a private reply channel.
func New(payload []byte) *Request {
	return &Request{Payload: payload, reply: make(chan Result, 1)}
}

// Done returns the channel on which the caller receives its own result.
func (r *Request) Done() <-chan Result { return r.reply }

// Resolve delivers the result, waking exactly this request's caller.
func (r *Request) Resolve(res Result) { r.reply <- res }
