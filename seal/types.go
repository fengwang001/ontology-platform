package seal

// SecondsPerDay defines the natural-day boundary. A day is [k*86400, (k+1)*86400).
const SecondsPerDay int64 = 86400

// SealStatus describes whether a seal may be used.
type SealStatus string

const (
	SealNormal   SealStatus = "NORMAL"
	SealDisabled SealStatus = "DISABLED"
)

// Seal has a category, a status and exactly two custodians.
type Seal struct {
	ID         string
	Category   string
	Status     SealStatus
	CustodianA string
	CustodianB string
}

// Grant associates an employee with a seal.
// Validity window is half-open: [ValidFrom, ValidUntil).
type Grant struct {
	ID         string
	Employee   string
	SealID     string
	Materials  map[string]struct{}
	AmountCap  int64 // maximum amount per single use (inclusive)
	DailyCap   int   // maximum successful uses per natural day (inclusive)
	ValidFrom  int64
	ValidUntil int64
	Revoked    bool
}

// Effective reports whether the grant covers time t.
func (g *Grant) Effective(t int64) bool {
	return !g.Revoked && t >= g.ValidFrom && t < g.ValidUntil
}

// AppStatus is the lifecycle state of an application.
type AppStatus string

const (
	AppPending  AppStatus = "PENDING"
	AppRejected AppStatus = "REJECTED"
	AppApproved AppStatus = "APPROVED"
	AppExecuted AppStatus = "EXECUTED"
	AppVoided   AppStatus = "VOIDED" // executed and later marked void
	AppDead     AppStatus = "DEAD"   // invalidated by seal disablement
)

// Application is a seal-use request.
type Application struct {
	ID         string
	Applicant  string
	SealID     string
	Material   string
	Amount     int64
	GrantID    string // grant bound at submission time
	Status     AppStatus
	Approvals  map[string]bool // approver -> approved (false only transiently; reject terminates)
	ApprovalAt int64           // time at which the last required approval was accepted
	ValidUntil int64           // ApprovalAt + ApprovalValidity (half-open)
	Presence   []string        // custodians who confirmed presence, in order
	ExecutedAt int64
	ReceiptAt  int64 // time the physical receipt was registered; 0 if not yet
	VoidedAt   int64
	Deadline   int64 // ExecutedAt + ReceiptWindow for executed applications
}

// Policy groups the fixed numeric thresholds and windows.
type Policy struct {
	// Approval tiers: amount < Tier1 needs 1 approver;
	// Tier1 <= amount < Tier2 needs 2; Tier2 <= amount needs 3 with a custodian.
	Tier1 int64
	Tier2 int64
	// DualPresence: amounts >= this require two distinct custodians at execution.
	DualPresence int64
	// ApprovalValidity: seconds from full approval to the exclusive execution deadline.
	ApprovalValidity int64
	// ReceiptWindow: seconds from execution during which the receipt must arrive.
	ReceiptWindow int64
}

// RequiredApprovals returns how many distinct non-applicant approvers are needed
// and whether at least one of them must be a custodian of the seal.
func (p Policy) RequiredApprovals(amount int64) (int, bool) {
	switch {
	case amount < p.Tier1:
		return 1, false
	case amount < p.Tier2:
		return 2, false
	default:
		return 3, true
	}
}

// LogEntry records one operation call, its outcome and the decision basis.
type LogEntry struct {
	Seq    int
	Now    int64
	Op     string
	Input  string
	Output string
	Basis  string
}

// Logger consumes structured decision logs.
type Logger interface {
	Log(entry LogEntry)
}
