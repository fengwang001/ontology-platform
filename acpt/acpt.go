// Package acpt implements the per-acceptor state machine of single-decree Paxos.
// It depends on no other package in this module.
package acpt

// Acceptor holds one acceptor's three quantities:
// promised is the highest proposal number it has promised to,
// accepted is the highest proposal number it has accepted (0 means never),
// acceptedValue is the value carried by that accepted proposal.
// The zero value is a fresh acceptor.
type Acceptor struct {
	promised      int
	accepted      int
	acceptedValue int
}

// Promise reports the highest proposal number promised to (0 if none).
func (a *Acceptor) Promise() int { return a.promised }

// Accepted reports the highest accepted proposal number (0 if never accepted).
func (a *Acceptor) Accepted() int { return a.accepted }

// AcceptedValue reports the accepted value; meaningful only when Accepted() > 0.
func (a *Acceptor) AcceptedValue() int { return a.acceptedValue }

// Prepare handles proposal number n.
// When n is strictly greater than the promised number it promises n and reports
// what it has accepted; otherwise it refuses without touching any state.
// Callers validate n > 0 before calling.
func (a *Acceptor) Prepare(n int) (ok bool, acceptedProposal, acceptedValue int) {
	if n <= a.promised {
		return false, a.accepted, a.acceptedValue
	}
	a.promised = n
	return true, a.accepted, a.acceptedValue
}

// Accept handles proposal (n, v).
// It accepts when n >= promised: the equality case must succeed, because an
// accept for the round this acceptor just promised to is valid.
// On success accepted=n and acceptedValue=v; on refusal no state changes.
func (a *Acceptor) Accept(n, v int) bool {
	if n < a.promised {
		return false
	}
	a.accepted = n
	a.acceptedValue = v
	return true
}
