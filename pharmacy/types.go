package pharmacy

type DrugConfig struct {
	PackSize   int
	Splittable bool
}

type DrugReport struct {
	OnHand            int
	ActiveReservation int
	OutstandingDebt   int
}

type LineInput struct {
	DrugID   string
	Quantity int
}

type PrescriptionInput struct {
	PatientID string
	IssuedAt  int
	AllAtOnce bool
	Lines     []LineInput
}

type LineReport struct {
	DrugID    string
	Demand    int
	Reserved  int
	Dispensed int
	Owed      int
	Complete  bool
}

type PrescriptionReport struct {
	ID        string
	PatientID string
	Status    string
	IssuedAt  int
	ExpiresAt int
	Lines     []LineReport
}

const (
	StatusActive    = "active"
	StatusCompleted = "completed"
	StatusCancelled = "cancelled"
	StatusExpired   = "expired"
)
