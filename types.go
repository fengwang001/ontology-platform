package parking

type ErrorCode string

const (
	ErrInvalidArgument  ErrorCode = "invalid_argument"
	ErrClockRolledBack  ErrorCode = "clock_rolled_back"
	ErrZoneNotFound     ErrorCode = "zone_not_found"
	ErrReservationGone  ErrorCode = "reservation_not_found"
	ErrReservationStale ErrorCode = "reservation_expired"
	ErrEarlyArrival     ErrorCode = "early_arrival"
	ErrDuplicateCheckIn ErrorCode = "duplicate_check_in"
	ErrVehicleCheckedIn ErrorCode = "vehicle_already_checked_in"
	ErrNoReassignment   ErrorCode = "no_spot_for_reassignment"
	ErrNoAvailableSpot  ErrorCode = "no_available_spot"
)

type Error struct {
	Code ErrorCode
	Msg  string
}

func (e Error) Error() string { return string(e.Code) + ": " + e.Msg }

type Time int64
type Duration int64
type Money int64

type SpotKind string

const (
	SpotNormal  SpotKind = "normal"
	SpotCharger SpotKind = "charger"
)

type ZoneConfig struct {
	ID                       string
	EarlyWindow              Duration
	GracePeriod              Duration
	RatePerSecond            Money
	OvertimeRatePerMinute    Money
	NoShowFee                Money
	ReassignmentCompensation Money
}

type Spot struct {
	ID     string
	ZoneID string
	Kind   SpotKind
}

type ReservationRequest struct {
	ReservationID string
	ZoneID        string
	Start         Time
	End           Time
	Vehicle       string
	NeedCharger   bool
}

type ReservationStatus string

const (
	StatusWaiting   ReservationStatus = "waiting"
	StatusReserved  ReservationStatus = "reserved"
	StatusOccupied  ReservationStatus = "occupied"
	StatusCancelled ReservationStatus = "cancelled"
	StatusExpired   ReservationStatus = "expired"
	StatusCompleted ReservationStatus = "completed"
)

type Reservation struct {
	ID             string
	ZoneID         string
	Vehicle        string
	SpotID         string
	Kind           SpotKind
	Start          Time
	End            Time
	BillingStart   Time
	NeedCharger    bool
	Status         ReservationStatus
	CreatedAt      Time
	ConvertedAt    Time
	CheckedInAt    Time
	CancelledAt    Time
	ExpiredAt      Time
	LeftAt         Time
	Reassigned     bool
	AssignedAt     Time
	IntervalStart  Time
	IntervalEnd    Time
	IntervalKeys   []Time
	CurrentKey     Time
	ConvertedFrom  string
	OriginalSpotID string
	PendingMove    bool
	BaseFee        Money
	OvertimeFee    Money
	NoShowFee      Money
	Compensation   Money
}

type ReserveResult struct {
	ReservationID string
	Status        ReservationStatus
	SpotID        string
	Reason        string
}

type WaitResult struct {
	ReservationID string
	Position      int
	Reason        string
}

type CheckInResult struct {
	ReservationID string
	SpotID        string
	Reason        string
}

type CancelResult struct {
	ReservationID string
	NoShowFee     Money
	Reason        string
}

type LeaveResult struct {
	ReservationID string
	BaseFee       Money
	OvertimeFee   Money
	Compensation  Money
	Total         Money
	Reason        string
}

type FeeBreakdown struct {
	ReservationID    string
	Vehicle          string
	ZoneID           string
	SpotID           string
	BaseFee          Money
	OvertimeFee      Money
	NoShowFee        Money
	CompensationPaid Money
	Total            Money
	OvertimeStart    Time
	LeftAt           Time
	OvertimeMinutes  int64
}

type SpotOccupancy struct {
	SpotID        string
	ZoneID        string
	Kind          SpotKind
	At            Time
	Occupied      bool
	ReservationID string
	Vehicle       string
	Status        ReservationStatus
}

type ActionResult struct {
	Kind          string
	At            Time
	OK            bool
	ErrorCode     ErrorCode
	Reason        string
	ReservationID string
	SpotID        string
	Position      int
	Fee           *FeeBreakdown
}
