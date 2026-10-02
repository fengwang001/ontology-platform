// Package ontology implements an ACME-style certificate order and
// authorization state machine with deterministic replay semantics.
package ontology

// Config holds the construction parameters of a Machine.
type Config struct {
	Ta int64 // lifetime of a pending authorization (seconds)
	Tv int64 // validity duration after a successful validation
	To int64 // order lifetime
	H  int64 // failure-rate-limit sliding window
	F  int   // failure threshold
	C  int64 // nonce pool capacity
	Pm int   // per-account pending-authorization quota
}

// Authz is the externally visible view of an authorization.
type Authz struct {
	ID      string
	Account []byte
	Ident   string
	Status  string // effective status
	Expires int64
}

// Order is the externally visible view of an order.
type Order struct {
	ID       string
	Account  []byte
	Idents   []string
	AuthzIDs []string
	Expires  int64
	Status   string // effective status
	CertSN   int64  // 0 when no certificate has been issued
}

// New constructs a Machine. It returns an *Error with Kind KindConfig when any
// construction parameter is outside its legal range.
func New(c Config) (*Machine, error) {
	if c.Ta < 1 || c.Ta > 1e9 || c.Tv < 1 || c.Tv > 1e9 ||
		c.To < 1 || c.To > 1e9 || c.H < 1 || c.H > 1e9 {
		return nil, errf(KindConfig, "durations must be in [1,1e9] seconds")
	}
	if c.F < 1 || c.F > 1000 {
		return nil, errf(KindConfig, "F must be in [1,1000]")
	}
	if c.C < 1 || c.C > 1e6 {
		return nil, errf(KindConfig, "C must be in [1,1e6]")
	}
	if c.Pm < 1 || c.Pm > 1e4 {
		return nil, errf(KindConfig, "Pm must be in [1,1e4]")
	}
	return newMachine(c), nil
}
