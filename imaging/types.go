package imaging

type DeviceClass string

const (
	DeviceClassCT DeviceClass = "CT"
	DeviceClassMR DeviceClass = "MR"
)

type Config struct {
	NormalRenalTTL      int
	HighRiskRenalTTL    int
	RenalLowLimit       int
	RenalHighLimit      int
	HydrationLead       int
	PremedicationLead   int
	ObservationDuration int
	ObservationCapacity int
	CleanupByClass      map[DeviceClass]int
}

type Device struct {
	ID         string
	Class      DeviceClass
	FieldLimit int
}

type QualityControl struct {
	Start int
	End   int
}

type ExamType struct {
	ID          string
	DeviceClass DeviceClass
	Duration    int
	Enhanced    bool
}

type Patient struct {
	ID                string
	HighRisk          bool
	ImplantFieldLimit int
	HasImplantLimit   bool
	ContrastAllergy   bool
}

type RenalResult struct {
	Value     int
	SampledAt int
	Sequence  int64
}

type AppointmentStatus string

const (
	StatusAccepted   AppointmentStatus = "accepted"
	StatusCheckedIn  AppointmentStatus = "checked_in"
	StatusReschedule AppointmentStatus = "needs_reschedule"
	StatusCancelled  AppointmentStatus = "cancelled"
)

type Appointment struct {
	ID               string
	DeviceID         string
	PatientID        string
	ExamID           string
	Start            int
	End              int
	OccupancyEnd     int
	Status           AppointmentStatus
	HydrationAt      int
	HasHydration     bool
	PremedAt         int
	HasPremed        bool
	IntervalSeq      int64
	IntervalPriority int64
}

type Receipt struct {
	Reason string
}
