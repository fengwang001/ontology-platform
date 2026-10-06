package railway

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func testConfig() TrainConfig {
	return TrainConfig{
		ID:             "T",
		Stations:       []string{"S0", "S1", "S2", "S3"},
		Departs:        []int64{100, 200, 300, 400},
		Seats:          []Seat{{Car: 1, No: 1}, {Car: 1, No: 2}, {Car: 2, No: 1}},
		Quota:          quotaMatrix(4, 10),
		SharedQuota:    10,
		AdvanceSeconds: 20,
		StandingRatio:  1,
	}
}

func quotaMatrix(stationCount, value int) [][]int {
	matrix := make([][]int, stationCount)
	for origin := range matrix {
		matrix[origin] = make([]int, stationCount)
		for destination := origin + 1; destination < stationCount; destination++ {
			matrix[origin][destination] = value
		}
	}
	return matrix
}

func mustBuy(t *testing.T, sys *System, req BuyRequest) BuyResult {
	t.Helper()
	result, err := sys.Buy(req)
	if err != nil {
		t.Fatalf("buy %+v failed: %v", req, err)
	}
	return result
}

func mustRefund(t *testing.T, sys *System, req RefundRequest) {
	t.Helper()
	if err := sys.Refund(req); err != nil {
		t.Fatalf("refund %+v failed: %v", req, err)
	}
}

func errorCodeIs(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if ErrorCodeOf(err) != want {
		t.Fatalf("error = %v, want code %d", err, want)
	}
}

func TestTouchingSegmentsReuseSameSeat(t *testing.T) {
	sys := NewSystem()
	if err := sys.AddTrain(testConfig()); err != nil {
		t.Fatal(err)
	}
	first := mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 1})
	second := mustBuy(t, sys, BuyRequest{Time: 2, TrainID: "T", TicketID: "b", Passenger: "B", Origin: 1, Destination: 2})
	if *first.Seat != *second.Seat {
		t.Fatalf("seats = %+v and %+v, want same seat", first.Seat, second.Seat)
	}
}

func TestSeatPreferenceBothOneNoneTouch(t *testing.T) {
	sys := NewSystem()
	if err := sys.AddTrain(testConfig()); err != nil {
		t.Fatal(err)
	}
	mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "l", Passenger: "L", Origin: 0, Destination: 1})
	mustBuy(t, sys, BuyRequest{Time: 2, TrainID: "T", TicketID: "r", Passenger: "R", Origin: 2, Destination: 3})
	middle := mustBuy(t, sys, BuyRequest{Time: 3, TrainID: "T", TicketID: "m", Passenger: "M", Origin: 1, Destination: 2})
	if middle.Seat.Car != 1 || middle.Seat.No != 1 {
		t.Fatalf("both-touch seat = %+v, want 1-1", middle.Seat)
	}

	mustBuy(t, sys, BuyRequest{Time: 4, TrainID: "T", TicketID: "l2", Passenger: "L2", Origin: 0, Destination: 1})
	oneTouch := mustBuy(t, sys, BuyRequest{Time: 5, TrainID: "T", TicketID: "x", Passenger: "X", Origin: 1, Destination: 2})
	if oneTouch.Seat.Car != 1 || oneTouch.Seat.No != 2 {
		t.Fatalf("one-touch seat = %+v, want 1-2", oneTouch.Seat)
	}

	mustBuy(t, sys, BuyRequest{Time: 6, TrainID: "T", TicketID: "r2", Passenger: "R2", Origin: 2, Destination: 3})
	noneTouch := mustBuy(t, sys, BuyRequest{Time: 7, TrainID: "T", TicketID: "y", Passenger: "Y", Origin: 1, Destination: 2})
	if noneTouch.Seat.Car != 2 || noneTouch.Seat.No != 1 {
		t.Fatalf("no-touch seat = %+v, want 2-1", noneTouch.Seat)
	}
}

func TestQuotaAllocationSharedAndExhaustion(t *testing.T) {
	cfg := testConfig()
	cfg.Quota = quotaMatrix(4, 0)
	cfg.SharedQuota = 1
	sys := NewSystem()
	if err := sys.AddTrain(cfg); err != nil {
		t.Fatal(err)
	}
	first := mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 2})
	if !first.UsedSharedQuota {
		t.Fatalf("first purchase did not use shared quota")
	}
	_, err := sys.Buy(BuyRequest{Time: 2, TrainID: "T", TicketID: "b", Passenger: "B", Origin: 0, Destination: 2, AcceptStanding: true})
	errorCodeIs(t, err, ErrQuotaExhausted)
}

func TestCutoffMergeIsAtDeadlineAndIrreversible(t *testing.T) {
	cfg := testConfig()
	cfg.Quota = quotaMatrix(4, 0)
	cfg.Quota[0][2] = 1
	cfg.SharedQuota = 0
	cfg.AdvanceSeconds = 20
	sys := NewSystem()
	if err := sys.AddTrain(cfg); err != nil {
		t.Fatal(err)
	}
	before, err := sys.RemainingQuota("T", 0, 2, 79)
	if err != nil || before.Allocation != 1 || before.Shared != 0 {
		t.Fatalf("before cutoff = %+v, %v", before, err)
	}
	at, err := sys.RemainingQuota("T", 0, 2, 80)
	if err != nil || at.Allocation != 0 || at.Shared != 1 {
		t.Fatalf("at cutoff = %+v, %v", at, err)
	}
	issued := mustBuy(t, sys, BuyRequest{Time: 80, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 2})
	if !issued.UsedSharedQuota {
		t.Fatalf("merged allocation was not charged from shared")
	}
	mustRefund(t, sys, RefundRequest{Time: 81, TrainID: "T", TicketID: "a"})
	remaining, err := sys.RemainingQuota("T", 0, 2, 81)
	if err != nil || remaining.Allocation != 0 || remaining.Shared != 1 {
		t.Fatalf("refund after merge = %+v, %v", remaining, err)
	}
}

func TestStandingCapacityAndForcedSeat(t *testing.T) {
	cfg := testConfig()
	cfg.StandingRatio = 0.34
	cfg.Quota = quotaMatrix(4, 10)
	cfg.SharedQuota = 10
	sys := NewSystem()
	if err := sys.AddTrain(cfg); err != nil {
		t.Fatal(err)
	}
	forceSeat := mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "force", Passenger: "A", Origin: 1, Destination: 2, AcceptStanding: true})
	if forceSeat.Standing || forceSeat.Seat == nil {
		t.Fatalf("available seat was not assigned: %+v", forceSeat)
	}
	mustBuy(t, sys, BuyRequest{Time: 2, TrainID: "T", TicketID: "s2", Passenger: "B", Origin: 1, Destination: 2})
	mustBuy(t, sys, BuyRequest{Time: 3, TrainID: "T", TicketID: "s3", Passenger: "C", Origin: 1, Destination: 2})
	standing := mustBuy(t, sys, BuyRequest{Time: 4, TrainID: "T", TicketID: "standing", Passenger: "D", Origin: 1, Destination: 2, AcceptStanding: true})
	if !standing.Standing {
		t.Fatalf("sold-out physical seats did not yield standing: %+v", standing)
	}
	_, err := sys.Buy(BuyRequest{Time: 5, TrainID: "T", TicketID: "stand2", Passenger: "E", Origin: 1, Destination: 2, AcceptStanding: true})
	errorCodeIs(t, err, ErrNoSeatAvailable)
}

func TestStandingFailsWhenOnlyPartOfSegmentIsFull(t *testing.T) {
	cfg := testConfig()
	cfg.StandingRatio = 1
	sys := NewSystem()
	if err := sys.AddTrain(cfg); err != nil {
		t.Fatal(err)
	}
	mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "seat1", Passenger: "A", Origin: 1, Destination: 2})
	mustBuy(t, sys, BuyRequest{Time: 2, TrainID: "T", TicketID: "seat2", Passenger: "B", Origin: 1, Destination: 2})
	mustBuy(t, sys, BuyRequest{Time: 3, TrainID: "T", TicketID: "seat3", Passenger: "C", Origin: 1, Destination: 2, AcceptStanding: true})
	for i := 0; i < len(cfg.Seats); i++ {
		mustBuy(t, sys, BuyRequest{Time: 4, TrainID: "T", TicketID: string(rune('a' + i)), Passenger: string(rune('a' + i)), Origin: 0, Destination: 1})
	}
	for i := 0; i < len(cfg.Seats); i++ {
		mustBuy(t, sys, BuyRequest{Time: 5, TrainID: "T", TicketID: string(rune('j' + i)), Passenger: string(rune('j' + i)), Origin: 0, Destination: 1, AcceptStanding: true})
	}
	_, err := sys.Buy(BuyRequest{Time: 6, TrainID: "T", TicketID: "long", Passenger: "D", Origin: 0, Destination: 3, AcceptStanding: true})
	errorCodeIs(t, err, ErrNoSeatAvailable)
}

func TestConcurrentPurchasesSerialize(t *testing.T) {
	sys := NewSystem()
	if err := sys.AddTrain(testConfig()); err != nil {
		t.Fatal(err)
	}
	const count = 60
	var wait sync.WaitGroup
	results := make(chan bool, count)
	wait.Add(count)
	for i := 0; i < count; i++ {
		i := i
		go func() {
			defer wait.Done()
			id := fmt.Sprintf("p%d", i)
			_, err := sys.Buy(BuyRequest{Time: 1, TrainID: "T", TicketID: id, Passenger: id, Origin: 0, Destination: 1})
			results <- err == nil
		}()
	}
	wait.Wait()
	close(results)
	accepted := 0
	for ok := range results {
		if ok {
			accepted++
		}
	}
	if accepted != len(testConfig().Seats) {
		t.Fatalf("accepted = %d, want exactly %d physical seats", accepted, len(testConfig().Seats))
	}
}

func TestPassengerTouchAndOverlap(t *testing.T) {
	sys := NewSystem()
	if err := sys.AddTrain(testConfig()); err != nil {
		t.Fatal(err)
	}
	mustBuy(t, sys, BuyRequest{Time: 1, TrainID: "T", TicketID: "a", Passenger: "P", Origin: 0, Destination: 2})
	mustBuy(t, sys, BuyRequest{Time: 2, TrainID: "T", TicketID: "b", Passenger: "P", Origin: 2, Destination: 3})
	_, err := sys.Buy(BuyRequest{Time: 3, TrainID: "T", TicketID: "c", Passenger: "P", Origin: 1, Destination: 2})
	errorCodeIs(t, err, ErrPassengerOverlap)
}

func TestExactlyAtDepartureIsRejected(t *testing.T) {
	sys := NewSystem()
	if err := sys.AddTrain(testConfig()); err != nil {
		t.Fatal(err)
	}
	_, err := sys.Buy(BuyRequest{Time: 100, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 1})
	errorCodeIs(t, err, ErrDeparted)
}

func TestRejectionPrecedenceAdjacentPairs(t *testing.T) {
	tests := []struct {
		name string
		run  func(*System) error
		want ErrorCode
	}{
		{"invalid before clock", func(sys *System) error {
			_, err := sys.Buy(BuyRequest{Time: -1, TrainID: "T"})
			return err
		}, ErrInvalidArgument},
		{"clock before train", func(sys *System) error {
			_, err := sys.Buy(BuyRequest{Time: 0, TrainID: "missing", TicketID: "x", Passenger: "P", Origin: 0, Destination: 1})
			return err
		}, ErrClockRewind},
		{"train before bad segment", func(sys *System) error {
			_, err := sys.Buy(BuyRequest{Time: 6, TrainID: "missing", TicketID: "x", Passenger: "P", Origin: 9, Destination: 10})
			return err
		}, ErrTrainNotFound},
		{"invalid segment before departure", func(sys *System) error {
			_, err := sys.Buy(BuyRequest{Time: 100, TrainID: "T", Origin: 9, Destination: 10})
			return err
		}, ErrInvalidArgument},
		{"departure before overlap", func(sys *System) error {
			_, err := sys.Buy(BuyRequest{Time: 100, TrainID: "T", TicketID: "z", Passenger: "A", Origin: 0, Destination: 1})
			return err
		}, ErrDeparted},
		{"overlap before quota", func(sys *System) error {
			mustBuy(t, sys, BuyRequest{Time: 6, TrainID: "T", TicketID: "p", Passenger: "A", Origin: 0, Destination: 2})
			_, err := sys.Buy(BuyRequest{Time: 7, TrainID: "T", TicketID: "p2", Passenger: "A", Origin: 0, Destination: 2})
			return err
		}, ErrPassengerOverlap},
		{"quota before seat", func(sys *System) error {
			cfg := testConfig()
			cfg.Quota = quotaMatrix(4, 0)
			cfg.SharedQuota = 0
			sys2 := NewSystem()
			_ = sys2.AddTrain(cfg)
			_, err := sys2.Buy(BuyRequest{Time: 1, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 3, AcceptStanding: true})
			return err
		}, ErrQuotaExhausted},
		{"refund missing before refunded", func(sys *System) error {
			return sys.Refund(RefundRequest{Time: 6, TrainID: "T", TicketID: "missing"})
		}, ErrTicketNotFound},
		{"missing train before missing ticket", func(sys *System) error {
			return sys.Refund(RefundRequest{Time: 6, TrainID: "missing", TicketID: "also-missing"})
		}, ErrTrainNotFound},
		{"refunded before departed", func(sys *System) error {
			mustBuy(t, sys, BuyRequest{Time: 6, TrainID: "T", TicketID: "a", Passenger: "A", Origin: 0, Destination: 1})
			mustRefund(t, sys, RefundRequest{Time: 7, TrainID: "T", TicketID: "a"})
			return sys.Refund(RefundRequest{Time: 100, TrainID: "T", TicketID: "a"})
		}, ErrTicketRefunded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sys := NewSystem()
			if err := sys.AddTrain(testConfig()); err != nil {
				t.Fatal(err)
			}
			_, _ = sys.Buy(BuyRequest{Time: 5, TrainID: "T", TicketID: "clock", Passenger: "Z", Origin: 0, Destination: 1})
			err := tt.run(sys)
			errorCodeIs(t, err, tt.want)
		})
	}
}

func TestErrorCodeType(t *testing.T) {
	var err error = RailwayError{Code: ErrDeparted}
	var railwayErr RailwayError
	if !errors.As(err, &railwayErr) || railwayErr.Code != ErrDeparted {
		t.Fatalf("error does not expose RailwayError: %v", err)
	}
}
