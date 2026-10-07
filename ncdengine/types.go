package ncdengine

type Config struct {
	RenewalGraceDays int
	AtFaultThreshold int
	ProtectionStart  int
	MaxLevel         int
	Premiums         []int
}

type RegisterInput struct {
	CustomerID string
	VehicleID  string
	StartDay   int
	Config     Config
}

type NewPolicyInput struct {
	CustomerID string
	VehicleID  string
	Day        int
}

type RenewInput struct {
	CustomerID string
	VehicleID  string
	Day        int
}

type ClaimInput struct {
	CustomerID  string
	VehicleID   string
	ClaimID     string
	AccidentDay int
	Liability   int
	ReportDay   int
}

type DeleteClaimInput struct {
	CustomerID string
	ClaimID    string
	Day        int
}

type ProtectInput struct {
	CustomerID string
	VehicleID  string
	Day        int
}

type TransferInput struct {
	CustomerID    string
	FromVehicleID string
	ToVehicleID   string
	Day           int
}

type Claim struct {
	ID          string
	AccidentDay int
	Liability   int
}

type PolicyTerm struct {
	VehicleID    string
	StartDay     int
	EndDay       int
	InitialLevel int
	RenewalLevel int
	Protection   bool
	Renewed      bool
}

type PremiumDelta struct {
	CustomerID   string
	ClaimID      string
	StartDay     int
	OldLevel     int
	NewLevel     int
	Amount       int
	DeletedClaim bool
}

type Result struct {
	Level    int
	StartDay int
	EndDay   int
}
