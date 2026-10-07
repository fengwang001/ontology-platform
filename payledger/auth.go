package payledger

// AuthStatus is the lifecycle state of an authorization at a given day.
type AuthStatus int

const (
	// StatusActive means the authorization is live: now <= expiryDay and
	// it was neither reversed nor final-captured.
	StatusActive AuthStatus = iota
	// StatusExpired means now > expiryDay and the authorization was never
	// explicitly terminated; its hold is automatically released.
	StatusExpired
	// StatusReversed means the authorization was explicitly reversed.
	StatusReversed
	// StatusFinalCaptured means a final capture terminated it.
	StatusFinalCaptured
)

func (s AuthStatus) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusExpired:
		return "expired"
	case StatusReversed:
		return "reversed"
	case StatusFinalCaptured:
		return "final-captured"
	default:
		return "unknown"
	}
}

// Authorization is the internal mutable state of one authorization.
type Authorization struct {
	id              string
	accountID       string
	totalAuthorized int64
	totalCaptured   int64
	totalRefunded   int64
	remainingHold   int64
	expiryDay       int64
	terminal        AuthStatus // StatusActive until reversed / final-captured
}

// status derives the lifecycle state at day now. Expiry is a pure function
// of (expiryDay, now): it never depends on any operation being triggered.
func (a *Authorization) status(now int64) AuthStatus {
	if a.terminal != StatusActive {
		return a.terminal
	}
	if now > a.expiryDay {
		return StatusExpired
	}
	return StatusActive
}

// captureCap is the maximum cumulative captured amount: the authorized
// amount inflated by toleranceBps basis points, uplift rounded down.
func (a *Authorization) captureCap(toleranceBps int64) int64 {
	return a.totalAuthorized + a.totalAuthorized*toleranceBps/10000
}

// AuthView is an immutable snapshot of an authorization at a given day.
type AuthView struct {
	ID              string
	AccountID       string
	Status          AuthStatus
	RemainingHold   int64
	TotalAuthorized int64
	TotalCaptured   int64
	TotalRefunded   int64
	ExpiryDay       int64
}

// view snapshots the authorization at day now. The remaining hold is
// reported at its effective value: an expired authorization holds nothing.
func (a *Authorization) view(now int64) AuthView {
	v := AuthView{
		ID:              a.id,
		AccountID:       a.accountID,
		Status:          a.status(now),
		RemainingHold:   a.remainingHold,
		TotalAuthorized: a.totalAuthorized,
		TotalCaptured:   a.totalCaptured,
		TotalRefunded:   a.totalRefunded,
		ExpiryDay:       a.expiryDay,
	}
	if v.Status != StatusActive {
		v.RemainingHold = 0
	}
	return v
}
