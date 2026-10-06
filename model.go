package lease

type LeaseStatus string

const (
	StatusActive       LeaseStatus = "active"
	StatusProtected    LeaseStatus = "protected"
	StatusMonthToMonth LeaseStatus = "month_to_month"
	StatusTerminated   LeaseStatus = "terminated"
)

type OfferStatus string

const (
	OfferOpen     OfferStatus = "open"
	OfferAccepted OfferStatus = "accepted"
	OfferRejected OfferStatus = "rejected"
	OfferExpired  OfferStatus = "expired"
)

type Party string

const (
	Landlord Party = "landlord"
	Tenant   Party = "tenant"
)

type Decision string

const (
	Accept  Decision = "accept"
	Reject  Decision = "reject"
	Counter Decision = "counter"
)

type Config struct {
	MinOfferDaysBeforeEnd int
	MaxOfferDaysBeforeEnd int
	ResponseDays          int
	TerminationNoticeDays int
	AnnualStepBasisPoints int
	CapBasisPoints        int
}

type CreateLeaseInput struct {
	ID               string
	StartDay         int
	EndDay           int
	MonthlyRentCents int
	LastAdjustmentAt int
}

type Lease struct {
	ID                     string
	StartDay               int
	EndDay                 int
	MonthlyRentCents       int
	LastAdjustmentAt       int
	EverReceivedOffer      bool
	TerminationNoticeAt    int
	TerminationEffectiveAt int
}

type Offer struct {
	ID               uint64
	LeaseID          string
	RentCents        int
	NewEndDay        int
	IssuedAt         int
	ResponseDue      int
	CounterRentCents int
	CounterAt        int
	CounterDue       int
	CounterUsed      bool
	Awaiting         Party
	Resolved         bool
	Accepted         bool
}

type Snapshot struct {
	Lease        Lease
	Status       LeaseStatus
	CurrentOffer *Offer
	OfferStatus  OfferStatus
}

type IssueOfferInput struct {
	LeaseID   string
	RentCents int
	NewEndDay int
}

type RespondInput struct {
	LeaseID          string
	OfferID          uint64
	Decision         Decision
	CounterRentCents int
}

type WithdrawInput struct {
	LeaseID string
	OfferID uint64
}

type NoticeInput struct {
	LeaseID string
}
