// Package acpt implements the local state of a single acceptor in a
// single-decree Paxos instance: the highest proposal it has promised, the
// highest proposal it has accepted, and the value accepted with it.
package acpt

// Acceptor is one acceptor's three quantities. The zero value is ready:
// nothing promised and nothing accepted (accepted == 0).
type Acceptor struct {
	promised      int
	accepted      int
	acceptedValue int
}

// New returns an acceptor that has made no promise and accepted no value.
func New() *Acceptor { return &Acceptor{} }

// Promise handles Prepare(n). It makes the promise only when n is strictly
// greater than the highest proposal already promised, and in either case
// reports what (if anything) it has accepted. A refused promise changes
// nothing.
func (a *Acceptor) Promise(n int) (ok bool, acceptedN int, v int) {
	if n <= a.promised {
		return false, a.accepted, a.acceptedValue
	}
	a.promised = n
	return true, a.accepted, a.acceptedValue
}

// Accept handles Accept(n, v). The comparison is >= on purpose: the accept
// belonging to the round that was just promised, where n == promised, must
// succeed. A stale accept (n < promised) is refused and changes nothing.
func (a *Acceptor) Accept(n, v int) bool {
	if n < a.promised {
		return false
	}
	a.accepted, a.acceptedValue = n, v
	return true
}

// Promised reports the highest proposal number promised (0 if none).
func (a *Acceptor) Promised() int { return a.promised }

// Accepted reports the highest proposal number accepted (0 if never).
func (a *Acceptor) Accepted() int { return a.accepted }

// AcceptedValue reports the value carried by Accepted (0 if never accepted).
func (a *Acceptor) AcceptedValue() int { return a.acceptedValue }
