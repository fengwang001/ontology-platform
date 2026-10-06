package apf

// Request describes an incoming server request. Seats is the number of
// concurrency seats the request occupies and must be in [1, 10].
type Request struct {
	User      string
	Namespace string
	Verb      string
	Resource  string
	Seats     int
}

// AdmitResult is the outcome of an accepted Admit call: either the request
// executes immediately (Lease != nil) or it is queued (Ticket != nil).
type AdmitResult struct {
	Lease  *Lease
	Ticket *Ticket
}

// Result is delivered through a Ticket when a queued request is dequeued for
// execution or rejected (e.g. on queue timeout).
type Result struct {
	Lease *Lease
	Err   error
}

// Ticket tracks a queued request. Exactly one Result is ever delivered.
type Ticket struct {
	ch chan Result
}

// C returns the channel on which the request's final Result is delivered.
func (t *Ticket) C() <-chan Result { return t.ch }

// Wait blocks until the request is dequeued or rejected.
func (t *Ticket) Wait() Result { return <-t.ch }

// resolve delivers the result; called at most once per ticket.
func (t *Ticket) resolve(r Result) { t.ch <- r }

// Lease represents an executing request. Finish releases its seats.
type Lease struct {
	ctrl  *Controller
	level *levelState // nil for exempt requests
	seats int
	done  bool
}
