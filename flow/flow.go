// Package flow implements credit-based point-to-point flow control between a
// single sender and a single receiver.
package flow

// Link is the shared flow-control link between one Sender and one Receiver.
type Link struct {
	// state fields are filled in later.
}

// Snapshot is an immutable view of link state, used by logs and tests.
type Snapshot struct {
	Capacity       int
	Buffer         int // messages currently held in the receiver buffer
	InFlightCredit int // credit granted but not yet consumed by sending
	Credit         int // sender-side spendable credit (equals InFlightCredit)
	Backlog        int // produced messages not sent yet
	Produced       int // total produced sequence numbers
	Sent           int // total sent sequence numbers
	Received       int // total messages received (equals Sent)
	Consumed       int // total consumed sequence numbers (contiguous)
}

// Sender is the sending endpoint of a Link.
type Sender struct{ link *Link }

// Receiver is the receiving endpoint of a Link.
type Receiver struct{ link *Link }

// Logger receives one structured line per decision step.
type Logger interface {
	Printf(format string, args ...any)
}

// NewLink creates a link with the given receiver-buffer capacity and sender
// backlog limit. Both must be positive.
func NewLink(capacity, backlogLimit int, logger Logger) (*Sender, *Receiver) {
	return nil, nil
}

// Produce appends messages to the backlog and automatically sends as many as
// current credit permits. Returns the number of messages sent in this call.
func (s *Sender) Produce(count int) (sent int, err error) {
	return 0, nil
}

// Probe performs a one-shot advertisement, but only when credit is zero and the
// backlog is non-empty.
func (s *Sender) Probe() (granted int, err error) {
	return 0, nil
}

// Advertise grants credit equal to (capacity - buffer - in-flight credit) when
// that value is positive; otherwise nothing is granted.
func (r *Receiver) Advertise() (granted int) {
	return 0
}

// Consume removes n contiguous messages from the receiver buffer in sequence
// number order. It never triggers an advertisement.
func (r *Receiver) Consume(n int) error {
	return nil
}

// Snapshot returns the current link state.
func (l *Link) Snapshot() Snapshot {
	return Snapshot{}
}
