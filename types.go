package ontology

import (
	"context"
	"errors"
	"sync"
)

type Money int64
type Position int
type Time int64

var (
	ErrInvalidArgument     = errors.New("invalid argument")
	ErrClockRollback       = errors.New("clock rollback")
	ErrVehicleNotFound     = errors.New("vehicle not found")
	ErrOrderNotFound       = errors.New("order not found")
	ErrOrderCompleted      = errors.New("order completed")
	ErrBoardedCannotCancel = errors.New("order boarded and cannot be cancelled")
	ErrPositionRollback    = errors.New("position rollback")
	ErrNoVehicleAvailable  = errors.New("no vehicle available")
)

type Config struct {
	PricePerUnit      Money
	StopDuration      Time
	TravelTimePerUnit Time
	MaxActiveOrders   int
	CancellationFee   Money
}

type VehicleInput struct {
	ID                string
	Seats             int
	Position          Position
	CurrentPassengers int
}

type OrderInput struct {
	ID           string
	BookTime     Time
	Pickup       Position
	Dropoff      Position
	People       int
	MaxDelay     Time
	LatestPickup Time
}

type OrderStatus string

const (
	StatusWaiting   OrderStatus = "waiting"
	StatusMatched   OrderStatus = "matched"
	StatusBoarded   OrderStatus = "boarded"
	StatusCompleted OrderStatus = "completed"
	StatusCancelled OrderStatus = "cancelled"
	StatusExpired   OrderStatus = "expired"
)

type SubmitResult struct {
	OrderID   string
	Status    OrderStatus
	VehicleID string
	Payable   Money
	Cap       Money
	Solo      Money
}

type CancelResult struct {
	OrderID string
	Status  OrderStatus
	Fee     Money
}

type PositionResult struct {
	VehicleID string
	Position  Position
	Completed []string
	Matched   []string
	Expired   []string
}

type OrderView struct {
	ID        string
	VehicleID string
	Status    OrderStatus
	Payable   Money
	Cap       Money
	Solo      Money
}

type LogEntry struct {
	Seq    int64
	Op     string
	Time   Time
	Input  map[string]any
	Output any
	Err    string
	Reason string
}

type Logger interface {
	Log(LogEntry)
}

type nopLogger struct{}

func (nopLogger) Log(LogEntry) {}

type Operation func(context.Context) error

type order struct {
	view      OrderView
	input     OrderInput
	bookIndex int64
	joinIndex int64
	onboard   bool
}

type vehicle struct {
	input    VehicleInput
	lastTime Time
	orderIDs map[string]struct{}
}

type Service struct {
	mu       sync.Mutex
	cfg      Config
	log      Logger
	clock    Time
	seq      int64
	vehicles map[string]*vehicle
	orders   map[string]*order
	waiting  []string
}

func NewService(cfg Config, log Logger) *Service {
	if log == nil {
		log = nopLogger{}
	}
	return &Service{cfg: cfg, log: log, vehicles: map[string]*vehicle{}, orders: map[string]*order{}}
}
