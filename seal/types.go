package seal

type StampStatus string

const (
	StampNormal   StampStatus = "normal"
	StampDisabled StampStatus = "disabled"
)

type ApplicationState string

const (
	StatePending     ApplicationState = "pending"
	StateRejected    ApplicationState = "rejected"
	StateApproved    ApplicationState = "approved"
	StateInvalidated ApplicationState = "invalidated"
	StateExecuted    ApplicationState = "executed"
)

type Config struct {
	ApprovalValiditySeconds int64
	ReceiptDeadlineSeconds  int64
	FirstAmountThreshold    int64
	SecondAmountThreshold   int64
	DualPresenceThreshold   int64
}

type Stamp struct {
	ID         string
	Category   string
	Status     StampStatus
	CustodianA string
	CustodianB string
}

type Authorization struct {
	ID            string
	EmployeeID    string
	StampID       string
	MaterialKinds map[string]struct{}
	MaxAmount     int64
	DailyLimit    int64
	StartsAt      int64
	EndsAt        int64
	Revoked       bool
}

type Application struct {
	ID           string
	ApplicantID  string
	StampID      string
	AuthID       string
	MaterialKind string
	Amount       int64
	State        ApplicationState
	Approvals    map[string]bool
	Confirmers   map[string]struct{}
	ExpiresAt    int64
	ExecutedAt   int64
	ReceiptDueAt int64
	ReceiptDone  bool
	Overdue      bool
	Voided       bool
}

type CreateStampInput struct {
	Now        int64
	StampID    string
	Category   string
	CustodianA string
	CustodianB string
}

type SetStampStatusInput struct {
	Now      int64
	StampID  string
	Disabled bool
}

type GrantAuthorizationInput struct {
	AuthorizationID string
	Now             int64
	EmployeeID      string
	StampID         string
	MaterialKinds   []string
	MaxAmount       int64
	DailyLimit      int64
	StartsAt        int64
	EndsAt          int64
}

type RevokeAuthorizationInput struct {
	Now             int64
	AuthorizationID string
}

type SubmitApplicationInput struct {
	Now           int64
	ApplicationID string
	ApplicantID   string
	StampID       string
	MaterialKind  string
	Amount        int64
}

type DecisionInput struct {
	Now           int64
	ApplicationID string
	ApproverID    string
	Approve       bool
}

type PresenceInput struct {
	Now           int64
	ApplicationID string
	CustodianID   string
}

type ApplicationInput struct {
	Now           int64
	ApplicationID string
}

type MarkVoidInput struct {
	Now           int64
	ApplicationID string
	CustodianID   string
}
