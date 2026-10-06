// Package parking implements deterministic partitioned parking reservations.
package parking

import "errors"

var (
	ErrInvalidArgument  = errors.New("参数非法")
	ErrClockRollback    = errors.New("时钟回退")
	ErrZoneNotFound     = errors.New("分区不存在")
	ErrReservationGone  = errors.New("预约不存在")
	ErrReservationDead  = errors.New("预约已失效")
	ErrEarlyArrival     = errors.New("早到")
	ErrDuplicateCheckIn = errors.New("重复核销")
	ErrVehicleOccupied  = errors.New("该车已有生效核销")
	ErrNoReassignment   = errors.New("无车位可改派")
	ErrNoAvailableSpot  = errors.New("无可用车位")
)

type SpotKind int

const (
	NormalSpot SpotKind = iota
	ChargingSpot
)

type ZoneConfig struct {
	EarlyWindow       int64
	GraceWindow       int64
	BaseRatePerMinute int64
	ChargingPerMinute int64
	OvertimePerMinute int64
	NoShowFee         int64
	OccupationPenalty int64
}

type Spot struct {
	Zone   string
	Number int
	Kind   SpotKind
}

type ReservationStatus int

const (
	StatusWaitlisted ReservationStatus = iota
	StatusReserved
	StatusCheckedIn
	StatusCanceled
	StatusExpired
	StatusDeparted
)

type Reservation struct {
	ID         string
	Zone       string
	Vehicle    string
	SpotNumber int
	Start      int64
	End        int64
	NeedCharge bool
	Status     ReservationStatus
	CheckInAt  int64
	DepartAt   int64
	CreatedAt  int64
	Reassigned bool
	Penalty    int64
	Award      int64
}

type FeeDetail struct {
	ReservationID     string
	Base              int64
	Charging          int64
	Overtime          int64
	OccupationPenalty int64
	CompensationAward int64
	NoShow            int64
	TotalCharged      int64
}

type Owner struct {
	ReservationID string
	Vehicle       string
}

type requestResult int

const (
	requestRejected requestResult = iota
	requestAccepted
)
