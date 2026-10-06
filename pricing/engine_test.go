package pricing

import (
	"errors"
	"testing"
	"time"
)

func testSchedule(weekdayRate, weekendRate, holidayRate Rate) Schedule {
	makePeriods := func(rate Rate) []Period {
		return []Period{{StartMinute: 0, EndMinute: 24 * 60, Rate: rate}}
	}
	return Schedule{
		Weekday: makePeriods(weekdayRate),
		Weekend: makePeriods(weekendRate),
		Holiday: makePeriods(holidayRate),
	}
}

func mustTime(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", value, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestIntervalCrossesDayVersionAndPeriodBoundaries(t *testing.T) {
	engine := NewEngine(time.UTC)
	v1 := TariffVersion{
		Effective: mustTime(t, "2024-01-01 00:00"),
		Schedule: Schedule{
			Weekday: {{0, 12 * 60, 10}, {12 * 60, 24 * 60, 20}},
			Weekend: {{0, 24 * 60, 20}},
			Holiday: {{0, 24 * 60, 30}},
		},
	}
	v2 := TariffVersion{
		Effective: mustTime(t, "2024-02-01 00:00"),
		Schedule: Schedule{
			Weekday: {{0, 24 * 60, 40}},
			Weekend: {{0, 24 * 60, 50}},
			Holiday: {{0, 24 * 60, 60}},
		},
	}
	if err := engine.RegisterTariff(v1); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterTariff(v2); err != nil {
		t.Fatal(err)
	}
	start := mustTime(t, "2024-01-31 23:00")
	end := mustTime(t, "2024-02-01 01:00")
	if err := engine.RegisterReading("p1", start, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p1", end, 4); err != nil {
		t.Fatal(err)
	}
	january, err := engine.Bill("p1", start)
	if err != nil {
		t.Fatal(err)
	}
	february, err := engine.Bill("p1", end)
	if err != nil {
		t.Fatal(err)
	}
	if got := january.totalEnergy(); got != 2 {
		t.Fatalf("january energy = %d, want 2", got)
	}
	if got := february.totalEnergy(); got != 2 {
		t.Fatalf("february energy = %d, want 2", got)
	}
	if got := february.Lines[0].Amount; got != 80 {
		t.Fatalf("february amount = %d, want 80", got)
	}
}

func TestHolidayOverridesWeekend(t *testing.T) {
	engine := NewEngine(time.UTC)
	if err := engine.RegisterTariff(TariffVersion{
		Effective: mustTime(t, "2024-03-01 00:00"),
		Schedule:  testSchedule(10, 20, 30),
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.SetHoliday("2024-03-02", true); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-03-02 00:00"), 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-03-03 00:00"), 5); err != nil {
		t.Fatal(err)
	}
	bill, err := engine.Bill("p", mustTime(t, "2024-03-02 00:00"))
	if err != nil {
		t.Fatal(err)
	}
	if bill.Lines[0].Rate != 30 || bill.Lines[0].Energy != 5 {
		t.Fatalf("bill = %+v, want holiday rate and energy 5", bill)
	}
}

func TestBackdatedTariffRecalculatesOpenMonth(t *testing.T) {
	engine := NewEngine(time.UTC)
	start := mustTime(t, "2024-04-01 00:00")
	end := mustTime(t, "2024-04-01 02:00")
	if err := engine.RegisterReading("p", start, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", end, 2); err != nil {
		t.Fatal(err)
	}
	before, err := engine.Bill("p", start)
	if err != nil {
		t.Fatal(err)
	}
	if before.UnpriceablePieces != 1 {
		t.Fatalf("unpriceable pieces = %d, want 1", before.UnpriceablePieces)
	}
	if err := engine.RegisterTariff(TariffVersion{
		Effective: start,
		Schedule:  testSchedule(7, 7, 7),
	}); err != nil {
		t.Fatal(err)
	}
	after, err := engine.Bill("p", start)
	if err != nil {
		t.Fatal(err)
	}
	if after.UnpriceablePieces != 0 || after.Lines[0].Amount != 14 {
		t.Fatalf("bill = %+v, want recalculated priced bill", after)
	}
}

func TestCorrectionBeforeCloseAndRejectionAfterClose(t *testing.T) {
	engine := NewEngine(time.UTC)
	january := mustTime(t, "2024-01-01 00:00")
	february := mustTime(t, "2024-02-01 00:00")
	if err := engine.RegisterTariff(TariffVersion{Effective: january, Schedule: testSchedule(3, 3, 3)}); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", january, 10); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", february, 20); err != nil {
		t.Fatal(err)
	}
	if err := engine.CorrectReading("p", january, 12); err != nil {
		t.Fatal(err)
	}
	if err := engine.CorrectReading("p", january, 21); errorKind(err) != KindRewind {
		t.Fatalf("error = %v, want reading rewind", err)
	}
	if err := engine.CloseMonth("p", january); err != nil {
		t.Fatal(err)
	}
	if err := engine.CorrectReading("p", january, 13); errorKind(err) != KindClosed {
		t.Fatalf("error = %v, want closed month", err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-01-15 00:00"), 30); errorKind(err) != KindClosed {
		t.Fatalf("error = %v, want closed month before order", err)
	}
	if err := engine.DeleteReading("p", january); errorKind(err) != KindClosed {
		t.Fatalf("error = %v, want closed month", err)
	}
}

func TestUnpriceablePieceAndReadingShortageBlockClose(t *testing.T) {
	engine := NewEngine(time.UTC)
	january := mustTime(t, "2024-01-15 00:00")
	if err := engine.RegisterReading("p", january, 0); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-01-16 00:00"), 1); err != nil {
		t.Fatal(err)
	}
	if err := engine.CloseMonth("p", january); errorKind(err) != KindShortage {
		t.Fatalf("error = %v, want shortage", err)
	}
	if err := engine.RegisterTariff(TariffVersion{
		Effective: mustTime(t, "2024-01-01 00:00"),
		Schedule:  testSchedule(1, 1, 1),
	}); err != nil {
		t.Fatal(err)
	}
	if err := engine.CloseMonth("p", january); errorKind(err) != KindShortage {
		t.Fatalf("error = %v, want reading shortage", err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-02-01 00:00"), 2); err != nil {
		t.Fatal(err)
	}
	if err := engine.CloseMonth("p", january); err != nil {
		t.Fatal(err)
	}
}

func TestRejectionOrderAndReadingRules(t *testing.T) {
	engine := NewEngine(time.UTC)
	january := mustTime(t, "2024-01-01 00:00")
	if err := engine.RegisterReading("", january, -1); errorKind(err) != KindInvalid {
		t.Fatalf("error = %v, want invalid", err)
	}
	if err := engine.RegisterReading("p", january, 10); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterReading("p", january, 11); errorKind(err) != KindOrder {
		t.Fatalf("error = %v, want order", err)
	}
	if err := engine.RegisterReading("p", mustTime(t, "2024-01-02 00:00"), 9); errorKind(err) != KindRewind {
		t.Fatalf("error = %v, want rewind", err)
	}
	if err := engine.SetHoliday("bad", true); errorKind(err) != KindInvalid {
		t.Fatalf("error = %v, want invalid date", err)
	}
	if err := engine.RegisterTariff(TariffVersion{Effective: january, Schedule: testSchedule(1, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	if err := engine.RegisterTariff(TariffVersion{Effective: january, Schedule: testSchedule(2, 2, 2)}); errorKind(err) != KindInvalid {
		t.Fatalf("error = %v, want duplicate version invalid", err)
	}
}

func errorKind(err error) ErrorKind {
	var domainError Error
	if errors.As(err, &domainError) {
		return domainError.Kind()
	}
	return ""
}
