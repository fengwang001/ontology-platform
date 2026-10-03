// Package report defines the drop-summary data types shared by the sampler.
package report

// Reason tells why a Summary was produced.
type Reason int

const (
	// Rolled: the entry's window expired and was reset by a newer record.
	Rolled Reason = iota
	// Evicted: the entry was evicted from a full per-tenant key table.
	Evicted
	// Closed: the entry's window is older than the Flush window.
	Closed
)

func (r Reason) String() string {
	switch r {
	case Rolled:
		return "Rolled"
	case Evicted:
		return "Evicted"
	case Closed:
		return "Closed"
	}
	return "Unknown"
}

// Summary accounts for dropped records of one (tenant, key) in one window.
// Window is the window number in which the drops happened.
type Summary struct {
	Tenant  string
	Key     string
	Window  uint64
	Dropped uint64
	Reason  Reason
}

// Sink receives summaries from Flush. A nil return commits the summary
// (the entry's dropped counter is cleared); a non-nil error aborts Flush.
type Sink func(Summary) error
