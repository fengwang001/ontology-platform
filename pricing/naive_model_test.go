package pricing

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
	"time"
)

type naiveModel struct {
	tariffs  []TariffVersion
	holidays map[string]struct{}
	points   map[string][]Reading
	closed   map[string]bool
}

func TestRandomOperationsMatchNaiveModel(t *testing.T) {
	engine := NewEngine(time.UTC)
	model := &naiveModel{holidays: map[string]struct{}{}, points: map[string][]Reading{}, closed: map[string]bool{}}
	random := rand.New(rand.NewSource(1460))
	lastMinute := map[string]int{}
	usedTariffs := map[int]bool{}

	for iteration := 0; iteration < 300; iteration++ {
		pointName := []string{"a", "b"}[random.Intn(2)]
		op := random.Intn(8)
		var input string
		var want error
		var got error

		switch {
		case op == 0:
			minute := random.Intn(46 * 24 * 60)
			if usedTariffs[minute] {
				continue
			}
			usedTariffs[minute] = true
			rate := Rate(1 + random.Intn(4))
			version := TariffVersion{Effective: naiveTime(minute), Schedule: testSchedule(rate, rate+10, rate+20)}
			input = fmt.Sprintf("tariff minute=%d rate=%d", minute, rate)
			want, got = model.tariff(version), engine.RegisterTariff(version)
		case op == 1:
			minute := random.Intn(32 * 24 * 60)
			date := naiveTime(minute).Format("2006-01-02")
			if date > "2024-02-01" {
				date = "2024-02-01"
			}
			holiday := random.Intn(2) == 0
			input = "holiday"
			want, got = model.setHoliday(date, holiday), engine.SetHoliday(date, holiday)
		case op < 6:
			minute := lastMinute[pointName] + random.Intn(1200)
			lastMinute[pointName] = minute
			readings := model.points[pointName]
			total := int64(0)
			if len(readings) > 0 {
				total = readings[len(readings)-1].Total + int64(random.Intn(7))
			}
			at := naiveTime(minute)
			input = fmt.Sprintf("register minute=%d total=%d", minute, total)
			want, got = model.register(pointName, at, total), engine.RegisterReading(pointName, at, total)
		case op == 6:
			readings := model.points[pointName]
			if len(readings) == 0 {
				continue
			}
			selected := random.Intn(len(readings))
			at := readings[selected].At
			low := int64(0)
			high := int64(1 << 30)
			if selected > 0 {
				low = readings[selected-1].Total
			}
			if selected+1 < len(readings) {
				high = readings[selected+1].Total
			}
			total := low
			if high > low {
				total += random.Int63n(high - low + 1)
			}
			input = fmt.Sprintf("correct minute=%d total=%d", at.Sub(naiveTime(0))/time.Minute, total)
			want, got = model.correct(pointName, at, total), engine.CorrectReading(pointName, at, total)
		default:
			readings := model.points[pointName]
			if len(readings) == 0 {
				continue
			}
			at := readings[random.Intn(len(readings))].At
			input = fmt.Sprintf("delete minute=%d", at.Sub(naiveTime(0))/time.Minute)
			want, got = model.delete(pointName, at), engine.DeleteReading(pointName, at)
		}

		if errorKind(want) != errorKind(got) {
			t.Fatalf("iter=%d %s want=%v got=%v", iteration, input, want, got)
		}
		t.Logf("iter=%d op=%s point=%s output=%v decision=%s", iteration, input, pointName, got, decision(want))

		if random.Intn(14) == 0 {
			want, got = model.close(pointName), engine.CloseMonth(pointName, naiveTime(0))
			if errorKind(want) != errorKind(got) {
				t.Fatalf("iter=%d close point=%s want=%v got=%v", iteration, pointName, want, got)
			}
		}

		naiveBill := model.bill(pointName, time.UTC)
		engineBill, err := engine.Bill(pointName, naiveTime(0))
		if err != nil {
			t.Fatal(err)
		}
		assertNaiveBill(t, iteration, pointName, engineBill, naiveBill)
	}
}

func naiveTime(minute int) time.Time {
	return time.Date(2024, 1, 1, 0, minute, 0, 0, time.UTC)
}

func decision(err error) string {
	if err == nil {
		return "accepted"
	}
	return string(errorKind(err))
}

func assertNaiveBill(t *testing.T, iteration int, pointName string, got Bill, want Bill) {
	t.Helper()
	if got.UnpriceableEnergy != want.UnpriceableEnergy {
		t.Fatalf("iter=%d point=%s unpriceable got=%d want=%d", iteration, pointName, got.UnpriceableEnergy, want.UnpriceableEnergy)
	}
	sort.Slice(got.Lines, func(i, j int) bool { return got.Lines[i].Rate < got.Lines[j].Rate })
	sort.Slice(want.Lines, func(i, j int) bool { return want.Lines[i].Rate < want.Lines[j].Rate })
	if len(got.Lines) != len(want.Lines) {
		t.Fatalf("iter=%d point=%s lines got=%+v want=%+v", iteration, pointName, got.Lines, want.Lines)
	}
	for i := range want.Lines {
		if got.Lines[i] != want.Lines[i] {
			t.Fatalf("iter=%d point=%s line got=%+v want=%+v", iteration, pointName, got.Lines[i], want.Lines[i])
		}
	}
}

func (m *naiveModel) anyClosed() bool {
	for _, closed := range m.closed {
		if closed {
			return true
		}
	}
	return false
}

func (m *naiveModel) tariff(version TariffVersion) error {
	for _, existing := range m.tariffs {
		if existing.Effective.Equal(version.Effective) {
			return errInvalid("duplicate version")
		}
	}
	if m.anyClosed() && version.Effective.Before(naiveTime(31*24*60)) {
		return errClosed
	}
	m.tariffs = append(m.tariffs, version)
	sort.Slice(m.tariffs, func(i, j int) bool { return m.tariffs[i].Effective.Before(m.tariffs[j].Effective) })
	return nil
}

func (m *naiveModel) setHoliday(date string, holiday bool) error {
	day, err := parseHolidayDate(date, time.UTC)
	if err != nil {
		return err
	}
	if m.anyClosed() && day.Before(naiveTime(31*24*60)) {
		return errClosed
	}
	if holiday {
		m.holidays[date] = struct{}{}
	} else {
		delete(m.holidays, date)
	}
	return nil
}

func (m *naiveModel) register(pointName string, at time.Time, total int64) error {
	readings := m.points[pointName]
	if len(readings) > 0 {
		last := readings[len(readings)-1]
		if m.closed[pointName] && at.Before(naiveTime(31*24*60)) {
			return errClosed
		}
		if !at.After(last.At) {
			return errOrder
		}
		if total < last.Total {
			return errRewind
		}
	}
	m.points[pointName] = append(readings, Reading{At: at, Total: total})
	return nil
}

func (m *naiveModel) correct(pointName string, at time.Time, total int64) error {
	index := m.readingIndex(pointName, at)
	readings := m.points[pointName]
	if index < 0 {
		return errOrder
	}
	if m.closed[pointName] && at.Before(naiveTime(31*24*60)) {
		return errClosed
	}
	if index > 0 && total < readings[index-1].Total {
		return errRewind
	}
	if index+1 < len(readings) && total > readings[index+1].Total {
		return errRewind
	}
	readings[index].Total = total
	return nil
}

func (m *naiveModel) delete(pointName string, at time.Time) error {
	index := m.readingIndex(pointName, at)
	if index < 0 {
		return errOrder
	}
	if m.closed[pointName] && at.Before(naiveTime(31*24*60)) {
		return errClosed
	}
	readings := m.points[pointName]
	updated := make([]Reading, 0, len(readings)-1)
	updated = append(updated, readings[:index]...)
	updated = append(updated, readings[index+1:]...)
	m.points[pointName] = updated
	return nil
}

func (m *naiveModel) readingIndex(pointName string, at time.Time) int {
	for index, reading := range m.points[pointName] {
		if reading.At.Equal(at) {
			return index
		}
	}
	return -1
}

func (m *naiveModel) close(pointName string) error {
	bill := m.bill(pointName, time.UTC)
	if bill.UnpriceablePieces > 0 {
		return errShortage
	}
	for _, reading := range m.points[pointName] {
		if !reading.At.Before(naiveTime(31 * 24 * 60)) {
			m.closed[pointName] = true
			return nil
		}
	}
	return errShortage
}

func (m *naiveModel) bill(pointName string, loc *time.Location) Bill {
	bill := Bill{}
	readings := m.points[pointName]
	monthEnd := naiveTime(31 * 24 * 60)
	for i := 0; i+1 < len(readings); i++ {
		from := readings[i]
		to := readings[i+1]
		if !to.At.After(naiveTime(0)) || !from.At.Before(monthEnd) {
			continue
		}
		for _, piece := range m.slice(from.At, to.At, to.Total-from.Total) {
			if piece.Start.Before(monthEnd) {
				if !piece.Priceable {
					bill.UnpriceableEnergy += piece.Energy
					bill.UnpriceablePieces++
				}
			}
		}
	}
	pieceByRate := map[Rate]Piece{}
	for i := 0; i+1 < len(readings); i++ {
		from, to := readings[i], readings[i+1]
		for _, piece := range m.slice(from.At, to.At, to.Total-from.Total) {
			if piece.Priceable && piece.Start.Before(monthEnd) {
				merged := pieceByRate[piece.Rate]
				merged.Rate = piece.Rate
				merged.Energy += piece.Energy
				merged.Amount += piece.Amount
				pieceByRate[piece.Rate] = merged
			}
		}
	}
	rates := make([]Rate, 0, len(pieceByRate))
	for rate := range pieceByRate {
		rates = append(rates, rate)
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i] < rates[j] })
	for _, rate := range rates {
		piece := pieceByRate[rate]
		if piece.Energy == 0 {
			continue
		}
		bill.Lines = append(bill.Lines, BillLine{Rate: rate, Energy: piece.Energy, Amount: piece.Amount})
	}
	return bill
}

func (m *naiveModel) slice(from, to time.Time, total int64) []Piece {
	boundaries := []time.Time{from, to}
	for _, version := range m.tariffs {
		if version.Effective.After(from) && version.Effective.Before(to) {
			boundaries = append(boundaries, version.Effective)
		}
	}
	day := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for day.Before(to) {
		if day.After(from) {
			boundaries = append(boundaries, day)
		}
		day = day.AddDate(0, 0, 1)
	}
	day = time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	for day.Before(to) {
		if version, ok := m.versionAt(day); ok {
			for dayType := Weekday; dayType <= Holiday; dayType++ {
				for _, period := range version.Schedule[dayType] {
					at := day.Add(time.Duration(period.StartMinute) * time.Minute)
					if at.After(from) && at.Before(to) {
						boundaries = append(boundaries, at)
					}
				}
			}
		}
		day = day.AddDate(0, 0, 1)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	unique := boundaries[:1]
	for _, boundary := range boundaries[1:] {
		if !boundary.Equal(unique[len(unique)-1]) {
			unique = append(unique, boundary)
		}
	}

	wholeDuration := int64(to.Sub(from).Minutes())
	pieces := make([]Piece, 0, len(unique)-1)
	allocated := int64(0)
	for i := 0; i+1 < len(unique); i++ {
		duration := int64(unique[i+1].Sub(unique[i]).Minutes())
		energy := total * duration / wholeDuration
		if i == len(unique)-2 {
			energy = total - allocated
		}
		allocated += energy
		piece := Piece{Start: unique[i], End: unique[i+1], Energy: energy}
		if version, ok := m.versionAt(piece.Start); ok {
			piece.Rate = periodRate(version.Schedule, m.dayType(piece.Start), piece.Start)
			piece.Amount = energy * int64(piece.Rate)
			piece.Priceable = true
		}
		pieces = append(pieces, piece)
	}
	return pieces
}

func (m *naiveModel) versionAt(at time.Time) (TariffVersion, bool) {
	index := sort.Search(len(m.tariffs), func(i int) bool { return m.tariffs[i].Effective.After(at) }) - 1
	if index < 0 {
		return TariffVersion{}, false
	}
	return m.tariffs[index], true
}

func (m *naiveModel) dayType(at time.Time) DayType {
	if _, ok := m.holidays[at.Format("2006-01-02")]; ok {
		return Holiday
	}
	if at.Weekday() == time.Saturday || at.Weekday() == time.Sunday {
		return Weekend
	}
	return Weekday
}
