package railway

import (
	"sort"
)

// Seat is the externally visible identity of one physical seat.
type Seat struct {
	Car int
	No  int
}

// TrainConfig describes one train and its immutable sale limits.
type TrainConfig struct {
	ID       string
	Stations []string
	Departs  []int64
	Seats    []Seat
	// Quota[origin][destination] is the initial station-pair allocation.
	Quota [][]int
	// SharedQuota is the train-wide quota used after an allocation is zero.
	SharedQuota int
	// AdvanceSeconds is subtracted from an origin's departure time to get its cutoff.
	AdvanceSeconds int64
	// StandingRatio is multiplied by the seat count and floored for every edge.
	StandingRatio float64
}

// BuyRequest is one purchase operation carrying its operation timestamp.
type BuyRequest struct {
	Time        int64
	TrainID     string
	TicketID    string
	Passenger   string
	Origin      int
	Destination int
	// AcceptStanding permits a standing ticket only if no seat is available.
	AcceptStanding bool
}

// BuyResult is deterministic for an accepted request.
type BuyResult struct {
	TicketID        string
	Seat            *Seat
	Standing        bool
	UsedSharedQuota bool
}

// RefundRequest releases one previously accepted ticket.
type RefundRequest struct {
	Time     int64
	TrainID  string
	TicketID string
}

type segment struct {
	origin      int
	destination int
}

type ticket struct {
	id          string
	trainID     string
	passenger   string
	origin      int
	destination int
	standing    bool
	seatIndex   int
	usedShared  bool
	refunded    bool
}

type train struct {
	id          string
	stations    []string
	departs     []int64
	seats       []Seat
	quota       [][]int
	sold        [][]int
	sharedQuota int
	advance     int64
	standCap    int
	standUsed   []int
	seating     *seatMap
	// mergedBefore means origins strictly below it have already merged.
	mergedBefore int
	tickets      map[string]*ticket
}

func validateConfig(cfg TrainConfig) error {
	if cfg.ID == "" || len(cfg.Stations) < 2 {
		return railwayError(ErrInvalidArgument, "invalid train identity or station list")
	}
	if len(cfg.Departs) != len(cfg.Stations) {
		return railwayError(ErrInvalidArgument, "departure count does not match station count")
	}
	for i, departure := range cfg.Departs {
		if departure < 0 || (i > 0 && departure <= cfg.Departs[i-1]) {
			return railwayError(ErrInvalidArgument, "departure times must be non-negative and strictly increasing")
		}
	}
	if len(cfg.Seats) == 0 {
		return railwayError(ErrInvalidArgument, "train must contain at least one seat")
	}
	for _, physicalSeat := range cfg.Seats {
		if physicalSeat.Car <= 0 || physicalSeat.No <= 0 {
			return railwayError(ErrInvalidArgument, "seat car and number must be positive")
		}
	}
	if cfg.SharedQuota < 0 {
		return railwayError(ErrInvalidArgument, "shared quota must be non-negative")
	}
	if len(cfg.Quota) != len(cfg.Stations) || len(cfg.Quota[0]) != len(cfg.Stations) {
		return railwayError(ErrInvalidArgument, "quota matrix must have station by station shape")
	}
	for origin := range cfg.Quota {
		if len(cfg.Quota[origin]) != len(cfg.Stations) {
			return railwayError(ErrInvalidArgument, "quota matrix must have station by station shape")
		}
		for destination, quota := range cfg.Quota[origin] {
			if quota < 0 || (origin >= destination && quota != 0) {
				return railwayError(ErrInvalidArgument, "quota must be non-negative and zero outside forward pairs")
			}
		}
	}
	if cfg.AdvanceSeconds < 0 || cfg.StandingRatio < 0 {
		return railwayError(ErrInvalidArgument, "advance and standing ratio must be non-negative")
	}
	if cfg.StandingRatio != cfg.StandingRatio || cfg.StandingRatio > 1 {
		return railwayError(ErrInvalidArgument, "standing ratio must be a finite number no greater than one")
	}
	return nil
}

func newTrain(cfg TrainConfig) (*train, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	seats := append([]Seat(nil), cfg.Seats...)
	sort.Slice(seats, func(i, j int) bool {
		if seats[i].Car != seats[j].Car {
			return seats[i].Car < seats[j].Car
		}
		return seats[i].No < seats[j].No
	})
	for i := 1; i < len(seats); i++ {
		if seats[i] == seats[i-1] {
			return nil, railwayError(ErrInvalidArgument, "duplicate seat identity")
		}
	}

	stationCount := len(cfg.Stations)
	quota := make([][]int, stationCount)
	sold := make([][]int, stationCount)
	for origin := 0; origin < stationCount; origin++ {
		quota[origin] = append([]int(nil), cfg.Quota[origin]...)
		sold[origin] = make([]int, stationCount)
	}

	return &train{
		id:          cfg.ID,
		stations:    append([]string(nil), cfg.Stations...),
		departs:     append([]int64(nil), cfg.Departs...),
		seats:       seats,
		quota:       quota,
		sold:        sold,
		sharedQuota: cfg.SharedQuota,
		advance:     cfg.AdvanceSeconds,
		standCap:    int(float64(len(seats)) * cfg.StandingRatio),
		standUsed:   make([]int, stationCount-1),
		seating:     newSeatMap(len(seats), stationCount),
		tickets:     make(map[string]*ticket),
	}, nil
}

func (tr *train) cutoff(origin int) int64 {
	return tr.departs[origin] - tr.advance
}
