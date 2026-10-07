package tolling

import (
	"fmt"
	"testing"
	"time"
)

func testNetwork() *Network {
	n := NewNetwork()
	for _, gate := range []string{"A", "B", "C", "D", "E"} {
		if err := n.AddGate(gate); err != nil {
			panic(err)
		}
	}
	add := func(from, to string, car, truck Money) {
		if err := n.AddEdge(Edge{From: from, To: to, Rates: map[string]Money{"car": car, "truck": truck}}); err != nil {
			panic(err)
		}
	}
	add("A", "B", 10, 20)
	add("A", "C", 10, 20)
	add("B", "D", 10, 20)
	add("C", "D", 10, 20)
	add("A", "D", 25, 50)
	add("D", "E", 10, 20)
	add("A", "E", 100, 200)
	return n
}

func testService(n *Network, cap Money, duplicateWindow, lateWindow time.Duration, loc *time.Location) *Service {
	return NewService(Config{DuplicateWindow: duplicateWindow, LateWindow: lateWindow, MonthlyCap: cap, TimeZone: loc}, n)
}

func mustRegisterVehicle(t *testing.T, s *Service, id, class string, at time.Time) {
	t.Helper()
	if err := s.RegisterVehicle(Vehicle{ID: id, InitialClass: class}, at); err != nil {
		t.Fatalf("RegisterVehicle: %v", err)
	}
}

func mustRecord(t *testing.T, s *Service, r GateRecord, at time.Time) OperationResult {
	t.Helper()
	result, err := s.RegisterGate(r, at)
	if err != nil {
		t.Fatalf("RegisterGate: %v", err)
	}
	return result
}

func at(minute int) time.Time {
	return time.Date(2026, 1, 15, 10, minute, 0, 0, time.UTC)
}

func TestEqualCostChoosesLexicographicallySmallestGateSequence(t *testing.T) {
	n := testNetwork()
	anchors := []anchor{{gate: "A", class: "car"}, {gate: "E", class: "car"}}
	path, fee, err := n.Reconstruct(anchors)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A", "B", "D", "E"}
	if fmt.Sprint(path) != fmt.Sprint(want) || fee != 30 {
		t.Fatalf("path=%v fee=%d, want %v 30", path, fee, want)
	}
}

func TestOrderedAnchorPath(t *testing.T) {
	n := testNetwork()
	path, _, err := n.Reconstruct([]anchor{{"A", "car"}, {"D", "car"}, {"E", "car"}})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"A", "B", "D", "E"}
	if fmt.Sprint(path) != fmt.Sprint(want) {
		t.Fatalf("path=%v want %v", path, want)
	}
	if _, _, err := n.Reconstruct([]anchor{{"E", "car"}, {"A", "car"}}); errorCode(err) != PathUnreachable {
		t.Fatalf("err=%v, want path_unreachable", err)
	}
}

func TestLateAtEntryAndExitBoundariesAndDeadline(t *testing.T) {
	for _, recordedMinute := range []int{0, 20} {
		t.Run(fmt.Sprintf("minute_%d", recordedMinute), func(t *testing.T) {
			s := testService(testNetwork(), 1000, time.Minute, 10*time.Minute, time.UTC)
			mustRegisterVehicle(t, s, "v", "car", at(-1))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(20))
			result := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "B", Kind: Intermediate, RecordedAt: at(recordedMinute)}, at(30))
			if result.RawAmount != 30 {
				t.Fatalf("raw=%d want 30", result.RawAmount)
			}
			if result.Adjustment == nil || result.Adjustment.Kind != NoAdjustment {
				t.Fatalf("adjustment=%+v, want no financial adjustment", result.Adjustment)
			}
		})
	}

	s := testService(testNetwork(), 1000, time.Minute, 10*time.Minute, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(20))
	result := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "D", Kind: Intermediate, RecordedAt: at(10)}, at(30).Add(time.Nanosecond))
	if result.Path != nil || result.Orphan {
		t.Fatalf("expired late was accepted into recomputation: %+v", result)
	}
	view, _ := s.Trip("tr")
	if view.RawAmount != 30 {
		t.Fatalf("raw after expired=%d want 30", view.RawAmount)
	}
}

func TestDuplicateWindowBoundary(t *testing.T) {
	s := testService(testNetwork(), 1000, 5*time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	first := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
	duplicate := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(4)}, at(1))
	independent := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr2", GateID: "A", Kind: Entry, RecordedAt: at(5)}, at(2))
	if first.Duplicate || !duplicate.Duplicate || independent.Duplicate {
		t.Fatalf("flags first=%v duplicate=%v independent=%v", first.Duplicate, duplicate.Duplicate, independent.Duplicate)
	}
}

func TestVehicleClassAtExactGateTime(t *testing.T) {
	n := NewNetwork()
	for _, gate := range []string{"A", "D", "E"} {
		if err := n.AddGate(gate); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []Edge{{From: "A", To: "D", Rates: map[string]Money{"car": 25, "truck": 50}}, {From: "D", To: "E", Rates: map[string]Money{"car": 10, "truck": 20}}} {
		if err := n.AddEdge(edge); err != nil {
			t.Fatal(err)
		}
	}
	s := testService(n, 1000, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	if err := s.ChangeVehicleClass("v", "truck", at(10), at(0)); err != nil {
		t.Fatal(err)
	}
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(1))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "D", Kind: Intermediate, RecordedAt: at(10)}, at(2))
	result := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(3))
	if result.RawAmount != 45 {
		t.Fatalf("raw=%d want car A-D 25 + truck D-E 20 = 45", result.RawAmount)
	}
}

func TestMonthlyCapSupplementaryTouchesCap(t *testing.T) {
	n := NewNetwork()
	for _, gate := range []string{"A", "B", "D", "E"} {
		if err := n.AddGate(gate); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []Edge{{From: "A", To: "E", Rates: map[string]Money{"car": 30}}, {From: "A", To: "D", Rates: map[string]Money{"car": 35}}, {From: "D", To: "E", Rates: map[string]Money{"car": 0}}, {From: "A", To: "B", Rates: map[string]Money{"car": 16}}, {From: "B", To: "D", Rates: map[string]Money{"car": 20}}} {
		if err := n.AddEdge(edge); err != nil {
			t.Fatal(err)
		}
	}
	s := testService(n, 30, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
	initial := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(1))
	if initial.Collected != 30 || initial.RawAmount != 30 {
		t.Fatalf("initial=%+v", initial)
	}
	supplement := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "D", Kind: Intermediate, RecordedAt: at(10)}, at(2))
	if supplement.RawAmount != 35 || supplement.Adjustment.Amount != 0 || supplement.Adjustment.CappedAfter != 5 {
		t.Fatalf("supplement=%+v", supplement.Adjustment)
	}
	month, _ := s.Month("v", monthKey(at(0), time.UTC))
	if month.Collected != 30 || month.CappedCharges != 5 {
		t.Fatalf("month=%+v", month)
	}
}

func TestRefundCanBringMonthlyCollectedExactlyToZero(t *testing.T) {
	n := NewNetwork()
	for _, gate := range []string{"A", "C", "E"} {
		if err := n.AddGate(gate); err != nil {
			t.Fatal(err)
		}
	}
	for _, edge := range []Edge{
		{From: "A", To: "E", Rates: map[string]Money{"car": 30, "truck": 0}},
		{From: "A", To: "C", Rates: map[string]Money{"car": 20, "truck": 0}},
		{From: "C", To: "E", Rates: map[string]Money{"car": 10, "truck": 0}},
	} {
		if err := n.AddEdge(edge); err != nil {
			t.Fatal(err)
		}
	}
	s := testService(n, 100, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(1))
	if err := s.ChangeVehicleClass("v", "truck", at(0), at(2)); err != nil {
		t.Fatal(err)
	}
	refund := mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "C", Kind: Intermediate, RecordedAt: at(10)}, at(3))
	if refund.RawAmount != 0 || refund.Adjustment.Kind != Refund || refund.Adjustment.Amount != 30 || refund.Adjustment.RawDelta != -30 {
		t.Fatalf("refund raw=%d adj=%+v", refund.RawAmount, refund.Adjustment)
	}
	month, _ := s.Month("v", monthKey(at(0), time.UTC))
	if month.Collected != 0 || month.BlockedRefunds != 0 {
		t.Fatalf("month=%+v", month)
	}
}

func TestUnorderedArrivalBeforeAndAfterSettlementMatches(t *testing.T) {
	run := func(settleAt int) (TripView, []Adjustment, MonthView) {
		s := testService(testNetwork(), 1000, time.Minute, 10*time.Hour, time.UTC)
		mustRegisterVehicle(t, s, "v", "car", at(-1))
		mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
		if settleAt > 2 {
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(2))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "D", Kind: Intermediate, RecordedAt: at(10)}, at(3))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "B", Kind: Intermediate, RecordedAt: at(5)}, at(4))
		} else {
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "D", Kind: Intermediate, RecordedAt: at(10)}, at(2))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "B", Kind: Intermediate, RecordedAt: at(5)}, at(3))
			mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(4))
		}
		view, err := s.Trip("tr")
		if err != nil {
			t.Fatal(err)
		}
		month, err := s.Month("v", monthKey(at(0), time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		return view, view.Adjustments, month
	}
	before, beforeAdjustments, beforeMonth := run(1)
	after, afterAdjustments, afterMonth := run(4)
	normalize := func(view TripView, adjustments []Adjustment) TripView {
		for i := range adjustments {
			adjustments[i].At = time.Time{}
		}
		view.Adjustments = adjustments
		return view
	}
	if fmt.Sprint(normalize(after, afterAdjustments)) != fmt.Sprint(normalize(before, beforeAdjustments)) {
		t.Fatalf("views differ:\nbefore=%+v\nafter=%+v", before, after)
	}
	if fmt.Sprint(afterMonth) != fmt.Sprint(beforeMonth) {
		t.Fatalf("months differ: %+v vs %+v", beforeMonth, afterMonth)
	}
}

func TestTripLookupDoesNotGrowWithVehicleHistory(t *testing.T) {
	n := testNetwork()
	s := testService(n, 1000, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-2))
	for i := 0; i < 200; i++ {
		entryAt := at(i * 60)
		exitAt := entryAt.Add(20 * time.Minute)
		mustRecord(t, s, GateRecord{VehicleID: "v", TripID: fmt.Sprintf("old-%d", i), GateID: "A", Kind: Entry, RecordedAt: entryAt}, entryAt.Add(time.Minute))
		mustRecord(t, s, GateRecord{VehicleID: "v", TripID: fmt.Sprintf("old-%d", i), GateID: "E", Kind: Exit, RecordedAt: exitAt}, exitAt.Add(time.Minute))
	}
	target := "target"
	entryAt := at(200 * 60)
	exitAt := entryAt.Add(20 * time.Minute)
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: target, GateID: "A", Kind: Entry, RecordedAt: entryAt}, entryAt.Add(time.Minute))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: target, GateID: "E", Kind: Exit, RecordedAt: exitAt}, exitAt.Add(time.Minute))
	result := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := s.Trip(target); err != nil {
				b.Fatal(err)
			}
		}
	})
	if result.AllocsPerOp() > 5 {
		t.Fatalf("Trip allocs/op=%v, lookup must remain independent of history", result.AllocsPerOp())
	}
}

func TestRejectedSettlementDoesNotAdvanceClock(t *testing.T) {
	n := NewNetwork()
	for _, gate := range []string{"A", "E"} {
		if err := n.AddGate(gate); err != nil {
			t.Fatal(err)
		}
	}
	s := testService(n, 1000, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(-1))
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(0)}, at(0))
	if _, err := s.RegisterGate(GateRecord{VehicleID: "v", TripID: "tr", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(1)); errorCode(err) != PathUnreachable {
		t.Fatalf("err=%v want path_unreachable", err)
	}
	if err := s.CloseTrip("tr", at(0)); err != nil {
		t.Fatalf("clock was advanced by rejected settlement: %v", err)
	}
}

func TestErrorPrecedenceAndMissingEntry(t *testing.T) {
	s := testService(testNetwork(), 1000, time.Minute, time.Hour, time.UTC)
	mustRegisterVehicle(t, s, "v", "car", at(10))
	if _, err := s.RegisterGate(GateRecord{VehicleID: "", TripID: "tr", GateID: "A", Kind: Entry, RecordedAt: at(20)}, at(5)); errorCode(err) != InvalidArgument {
		t.Fatalf("err=%v want invalid_argument before clock", err)
	}
	if _, err := s.RegisterGate(GateRecord{VehicleID: "v", TripID: "missing", GateID: "E", Kind: Exit, RecordedAt: at(20)}, at(11)); errorCode(err) != MissingEntry {
		t.Fatalf("err=%v want missing_entry", err)
	}
	if _, err := s.Adjustments("missing"); errorCode(err) != TripNotFound {
		t.Fatalf("err=%v want trip_not_found", err)
	}
	mustRecord(t, s, GateRecord{VehicleID: "v", TripID: "open", GateID: "A", Kind: Entry, RecordedAt: at(12)}, at(12))
	if _, err := s.Adjustments("open"); errorCode(err) != TripNotSettled {
		t.Fatalf("err=%v want trip_not_settled", err)
	}
}
