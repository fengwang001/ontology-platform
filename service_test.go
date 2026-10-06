package parking

import (
	"errors"
	"testing"
)

func testConfig() ([]ZoneConfig, []Spot) {
	zones := []ZoneConfig{{
		ID:                       "Z",
		EarlyWindow:              60,
		GracePeriod:              60,
		RatePerSecond:            1,
		OvertimeRatePerMinute:    100,
		NoShowFee:                7,
		ReassignmentCompensation: 9,
	}}
	spots := []Spot{{ID: "N1", ZoneID: "Z", Kind: SpotNormal}, {ID: "N2", ZoneID: "Z", Kind: SpotNormal}, {ID: "C1", ZoneID: "Z", Kind: SpotCharger}}
	return zones, spots
}

func reserve(t *testing.T, s *Service, at Time, start, end Time, vehicle string, charger bool) ReserveResult {
	t.Helper()
	result, err := s.Reserve(at, ReservationRequest{ZoneID: "Z", Start: start, End: end, Vehicle: vehicle, NeedCharger: charger})
	if err != nil {
		t.Fatalf("Reserve(%s): %v", vehicle, err)
	}
	return result
}

func errorCode(err error) ErrorCode {
	var e Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestAdjacentIntervalsDoNotOverlap(t *testing.T) {
	s, err := NewService(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	first := reserve(t, s, 0, 100, 200, "A", false)
	second := reserve(t, s, 0, 200, 300, "B", false)
	if first.SpotID != second.SpotID {
		t.Fatalf("adjacent reservations should share spot, got %s and %s", first.SpotID, second.SpotID)
	}
	occupy, err := s.SpotOccupantAt(first.SpotID, 200)
	if err != nil {
		t.Fatal(err)
	}
	if occupy.Vehicle != "B" {
		t.Fatalf("at boundary got %q", occupy.Vehicle)
	}
}

func TestCheckInWindowEndpoints(t *testing.T) {
	s, _ := NewService(testConfig())
	r := reserve(t, s, 0, 100, 200, "A", false)
	if _, err := s.CheckIn(39, r.ReservationID); errorCode(err) != ErrEarlyArrival {
		t.Fatalf("before window err=%v", err)
	}
	if _, err := s.CheckIn(40, r.ReservationID); err != nil {
		t.Fatalf("window start: %v", err)
	}

	s2, _ := NewService(testConfig())
	r2 := reserve(t, s2, 0, 100, 200, "B", false)
	if _, err := s2.CheckIn(160, r2.ReservationID); err != nil {
		t.Fatalf("window end: %v", err)
	}
}

func TestExpirationAndWaitRegistrationAtSameTime(t *testing.T) {
	s, _ := NewService(testConfig())
	first := reserve(t, s, 0, 100, 200, "A", false)
	reserve(t, s, 0, 100, 200, "N", false)
	reserve(t, s, 0, 100, 200, "C", true)
	wait, err := s.RegisterWait(0, ReservationRequest{ZoneID: "Z", Start: 100, End: 200, Vehicle: "B", NeedCharger: false})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Reserve(160, ReservationRequest{ZoneID: "Z", Start: 500, End: 600, Vehicle: "C"}); err != nil {
		t.Fatalf("advance clock: %v", err)
	}
	expiredFirst, err := s.Reservation(first.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	if expiredFirst.Status != StatusExpired {
		t.Fatalf("status=%s", expiredFirst.Status)
	}
	converted, err := s.Reservation(wait.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Status != StatusReserved || converted.SpotID != first.SpotID {
		t.Fatalf("waiter not promoted: %+v", converted)
	}
	_, err = s.RegisterWait(160, ReservationRequest{ZoneID: "Z", Start: 161, End: 200, Vehicle: "D"})
	if errorCode(err) != ErrInvalidArgument {
		t.Fatalf("later empty interval must not be wait-only, err=%v", err)
	}
}

func TestOvertimeExactMinuteAndExtraSecond(t *testing.T) {
	for _, tc := range []struct {
		name  string
		leave Time
		want  Money
	}{{"exact", 260, 100}, {"extra-second", 261, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := NewService(testConfig())
			r := reserve(t, s, 0, 100, 200, "A", false)
			if _, err := s.CheckIn(100, r.ReservationID); err != nil {
				t.Fatal(err)
			}
			result, err := s.Leave(tc.leave, r.ReservationID)
			if err != nil {
				t.Fatal(err)
			}
			if result.OvertimeFee != tc.want {
				t.Fatalf("overtime fee=%d want=%d", result.OvertimeFee, tc.want)
			}
		})
	}
}

func TestOvertimeReassignsNormalToCharger(t *testing.T) {
	s2, _ := NewService(testConfig())
	reserve(t, s2, 0, 300, 400, "F", false)
	n := reserve(t, s2, 0, 0, 200, "N", false)
	reserve(t, s2, 0, 250, 350, "P", false)
	victim := reserve(t, s2, 0, 200, 300, "V", false)
	if _, err := s2.CheckIn(0, n.ReservationID); err != nil {
		t.Fatal(err)
	}
	leave, err := s2.Leave(201, n.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	if leave.Compensation != 9 {
		t.Fatalf("compensation=%d", leave.Compensation)
	}
	converted, err := s2.Reservation(victim.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Status != StatusReserved || converted.SpotID != "C1" {
		t.Fatalf("reassignment=%+v", converted)
	}
}

func TestWaitlistSkipsIncompatibleIntervals(t *testing.T) {
	s, _ := NewService(testConfig())
	first := reserve(t, s, 0, 100, 200, "A", false)
	reserve(t, s, 0, 100, 200, "O", false)
	reserve(t, s, 0, 100, 200, "E", true)
	long, err := s.RegisterWait(0, ReservationRequest{ZoneID: "Z", Start: 100, End: 300, Vehicle: "L"})
	if err != nil {
		t.Fatal(err)
	}
	match, err := s.RegisterWait(0, ReservationRequest{ZoneID: "Z", Start: 100, End: 200, Vehicle: "M"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(100, first.ReservationID); err != nil {
		t.Fatal(err)
	}
	skipped, _ := s.Reservation(long.ReservationID)
	promoted, _ := s.Reservation(match.ReservationID)
	if skipped.Status != StatusWaiting || promoted.Status != StatusReserved || promoted.SpotID != first.SpotID {
		t.Fatalf("skip promotion failed: skipped=%+v promoted=%+v", skipped, promoted)
	}
}

func TestCancelAtStartChargesNoShow(t *testing.T) {
	s, _ := NewService(testConfig())
	r := reserve(t, s, 0, 100, 200, "A", false)
	result, err := s.Cancel(100, r.ReservationID)
	if err != nil {
		t.Fatal(err)
	}
	if result.NoShowFee != 7 {
		t.Fatalf("fee=%d", result.NoShowFee)
	}
}

func TestSecondCheckInForOverlappingReservationRejected(t *testing.T) {
	s, _ := NewService(testConfig())
	first := reserve(t, s, 0, 100, 200, "A", false)
	second := reserve(t, s, 0, 100, 200, "A", true)
	if _, err := s.CheckIn(100, first.ReservationID); err != nil {
		t.Fatal(err)
	}
	_, err := s.CheckIn(100, second.ReservationID)
	if errorCode(err) != ErrVehicleCheckedIn {
		t.Fatalf("err=%v", err)
	}
}
