package lease

// Party is an opaque party identifier. The landlord of a chain is tracked
// separately on the master lease.
type Party int64

// LeaseID identifies a lease. IDs are supplied by the caller so identical
// operation sequences replay identically.
type LeaseID int64

// Lease is one node of a sublease chain.
type Lease struct {
	ID         LeaseID
	Landlord   Party
	Tenant     Party
	Parent     LeaseID // 0 for a master lease / direct lease
	Start      int
	End        int // exclusive: start day in lease, end day not
	Rent       int // monthly rent, integer units
	Active     bool
	Depth      int // 1 = master/direct lease, increasing down the chain
	Root       LeaseID
	Recognized bool // independent landlord recognition
}

// Payment records one (partial) settlement of an Arrears.
type Payment struct {
	Payer  Party
	Lease  LeaseID // level at which the payer paid
	Amount int
	Day    int
}

// Arrears is one overdue monthly installment.
type Arrears struct {
	ID     int
	Lease  LeaseID
	Root   LeaseID
	Due    int
	Amount int
	Paid   int
	// Chain is the liability chain fixed at creation: [debtor lease, ..., master].
	Chain    []LeaseID
	Payments []Payment
}

// Recourse is the reimbursement claim created by a settling level against the
// actual debtor. One Payment creates exactly one Recourse of equal amount.
type Recourse struct {
	ArrearsID int
	From      Party // actual debtor, must reimburse
	To        Party // level that settled
	Amount    int
	Day       int
}

// Consent records landlord consent for a future sublease.
type Consent struct {
	OneShot  bool
	Landlord Party
	Lease    LeaseID // one-shot: the sublease id it authorizes
	Tenant   Party   // general: the tenant whose subleases it covers
	Root     LeaseID
	Granted  int
	Revoked  bool
	Consumed bool // one-shot: consumed once the sublease is established
}

// Config holds system-wide parameters.
type Config struct {
	RentFactorPct int // sub-rent <= parent rent * this / 100
	MaxDepth      int
	GraceDays     int // overdue by G days raises arrears
	PayDay        int // fixed payment day index within every 30-day month
}

// Snapshot is the full comparable state used in differential tests.
type Snapshot struct {
	Leases    map[LeaseID]Lease
	Arrears   []Arrears
	Recourses []Recourse
	Consents  []Consent
	Now       int
}

// Operation inputs. Every mutating action carries the caller's current now.

type CreateMasterOp struct {
	ID               LeaseID
	Landlord, Tenant Party
	Start, End, Rent int
	Now              int
}

type CreateSubleaseOp struct {
	ID                    LeaseID
	Parent                LeaseID
	Tenant                Party
	Start, End, Rent, Now int
}

type GrantOneShotOp struct {
	ID       LeaseID // sublease id the consent authorizes
	Landlord Party
	Root     LeaseID
	Now      int
}

type GrantGeneralOp struct {
	Landlord Party
	Tenant   Party
	Root     LeaseID
	Now      int
}

type RevokeGeneralOp struct {
	Landlord Party
	Tenant   Party
	Root     LeaseID
	Now      int
}

type RecognizeOp struct {
	Lease    LeaseID
	Landlord Party
	Now      int
}

type PayOp struct {
	ArrearsID int
	By        Party
	AtLease   LeaseID // chain level the payer acts as
	Amount    int
	Now       int
}

type TerminateOp struct {
	Lease LeaseID
	By    Party
	Day   int // 0 means now
	Now   int
}

type ExitOp struct {
	Lease LeaseID
	By    Party
	Now   int
}

type AdvanceOp struct{ Now int }

// Code identifies a domain error. Error reporting is ordered: the service
// checks conditions in the fixed order of the Err* constants below and only
// reports the first applicable error.
type Code int

const (
	ErrInvalid Code = iota + 1
	ErrClockRollback
	ErrLeaseNotFound
	ErrNoConsent
	ErrTermOutOfRange
	ErrRentExceedsLimit
	ErrDepthExceeded
	ErrStateNotAllowed
)

var codeNames = [...]string{
	"",
	"invalid argument",
	"clock rollback",
	"lease not found or terminated",
	"landlord consent missing",
	"sub-term exceeds parent term",
	"sub-rent exceeds factor limit",
	"chain depth exceeded",
	"operation not allowed in this state",
}

func (c Code) String() string {
	if int(c) < 0 || int(c) >= len(codeNames) {
		return "unknown error"
	}
	return codeNames[c]
}

// OpError is the only error type returned by the service.
type OpError struct{ Code Code }

func (e *OpError) Error() string { return e.Code.String() }

func fail(c Code) error { return &OpError{Code: c} }
