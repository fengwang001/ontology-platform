// Package delivery models a delivery-exception reporting and undeliverable
// disposition system with globally monotonic integer-second clocks.
package delivery

// ExceptionType is the kind of a reported delivery exception.
type ExceptionType int

const (
	// ExceptionUnreachable means the rider cannot reach the customer.
	ExceptionUnreachable ExceptionType = iota + 1
	// ExceptionWrongAddress means the delivery address is wrong.
	ExceptionWrongAddress
	// ExceptionRefusal means the customer refuses to accept the order.
	ExceptionRefusal
)

// ExceptionPhase is the lifecycle phase of one exception instance.
type ExceptionPhase int

const (
	// ExceptionOpen means the exception is still in progress.
	ExceptionOpen ExceptionPhase = iota + 1
	// ExceptionClosed means the exception was resolved and delivery resumes.
	ExceptionClosed
	// ExceptionUndeliverable means the exception became undeliverable.
	ExceptionUndeliverable
)

// OrderStatus is the lifecycle status of an order.
type OrderStatus int

const (
	// OrderCreated means the order was placed but not yet picked up.
	OrderCreated OrderStatus = iota + 1
	// OrderDelivering means the rider picked up the order and is delivering.
	OrderDelivering
	// OrderDelivered means the order was delivered successfully.
	OrderDelivered
	// OrderAwaitingCorrection means a wrong-address exception awaits correction.
	OrderAwaitingCorrection
	// OrderReturning means the order is on its way back to the merchant.
	OrderReturning
	// OrderReturned is a terminal state: merchant confirmed the return.
	OrderReturned
	// OrderReturnUnconfirmed is a terminal state: merchant missed the window.
	OrderReturnUnconfirmed
	// OrderDisposed is a terminal state: handled on site per merchant preset.
	OrderDisposed
)

// Disposition is the merchant's preset handling of an undeliverable order.
type Disposition int

const (
	// DispositionReturn sends the order back to the merchant.
	DispositionReturn Disposition = iota + 1
	// DispositionOnSite handles the goods on site.
	DispositionOnSite
)

// Liability identifies who bears the loss of an undeliverable order.
type Liability int

const (
	// LiabilityNone means no undeliverable loss has been attributed yet.
	LiabilityNone Liability = iota
	// LiabilityCustomer means the customer is responsible.
	LiabilityCustomer
	// LiabilityMerchant means the merchant is responsible.
	LiabilityMerchant
)

// Address is an integer-grid point; distance is Manhattan distance.
type Address struct {
	X int
	Y int
}

// Distance returns the Manhattan distance between two addresses.
func (a Address) Distance(other Address) int {
	dx := a.X - other.X
	if dx < 0 {
		dx = -dx
	}
	dy := a.Y - other.Y
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

// Config holds every tunable timing and policy parameter.
type Config struct {
	MinWaitSeconds        int
	MinContacts           int
	MinContactInterval    int
	CorrectionWindow      int
	MaxCorrectionDist     int
	EvidenceTTLSeconds    int
	RiderCompensation     int
	MerchantConfirmWindow int
}

// Exception is an externally visible snapshot of one exception instance.
type Exception struct {
	ID           int
	Type         ExceptionType
	Phase        ExceptionPhase
	Start        int
	ContactCount int
	WindowEnd    int
	EvidenceID   string
	EvidenceTime int
}

// Order is an externally visible snapshot of an order.
type Order struct {
	ID           string
	Status       OrderStatus
	Address      Address
	Disposition  Disposition
	Liability    Liability
	AddrChanges  int
	CompPaid     bool
	ExceptionSeq int
	Current      *Exception
	WindowEnd    int
}
