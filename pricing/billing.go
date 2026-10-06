package pricing

import (
	"math/big"
	"sort"
	"time"
)

type Energy = int64
type Money = int64

type BillLine struct {
	Rate   Rate
	Energy Energy
	Amount Money
}

type Bill struct {
	Lines             []BillLine
	UnpriceableEnergy Energy
	UnpriceablePieces int
}

type Piece struct {
	Start     time.Time
	End       time.Time
	Energy    Energy
	Amount    Money
	Rate      Rate
	DayType   DayType
	Priceable bool
}

func monthKey(at time.Time) int { return at.Year()*12 + int(at.Month()) - 1 }

func monthRange(key int, loc *time.Location) (time.Time, time.Time) {
	start := time.Date(key/12, time.Month(key%12+1), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0)
}

func (b *Bill) totalEnergy() Energy {
	total := b.UnpriceableEnergy
	for _, line := range b.Lines {
		total += line.Energy
	}
	return total
}

func sliceInterval(start, end time.Time, total Energy, tariffs *tariffBook, cal *calendar) ([]Piece, error) {
	if !start.Before(end) {
		return nil, nil
	}
	boundaries := []time.Time{start, end}
	firstVersion := sort.Search(len(tariffs.versions), func(i int) bool {
		return tariffs.versions[i].Effective.After(start)
	}) - 1
	if firstVersion < 0 {
		firstVersion = sort.Search(len(tariffs.versions), func(i int) bool {
			return !tariffs.versions[i].Effective.Before(start)
		})
	}
	lastVersion := sort.Search(len(tariffs.versions), func(i int) bool {
		return !tariffs.versions[i].Effective.Before(end)
	})
	for index := firstVersion; index >= 0 && index < lastVersion; index++ {
		effective := tariffs.versions[index].Effective
		if effective.After(start) && effective.Before(end) {
			boundaries = append(boundaries, effective)
		}
	}
	dayStart := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	for dayStart.Before(end) {
		nextDay := dayStart.AddDate(0, 0, 1)
		if dayStart.After(start) && dayStart.Before(end) {
			boundaries = append(boundaries, dayStart)
		}
		dayStart = nextDay
	}
	firstDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	for versionIndex := firstVersion; versionIndex >= 0 && versionIndex < lastVersion; versionIndex++ {
		version := tariffs.versions[versionIndex]
		applicableEnd := end
		if versionIndex+1 < len(tariffs.versions) {
			if nextEffective := tariffs.versions[versionIndex+1].Effective; nextEffective.Before(applicableEnd) {
				applicableEnd = nextEffective
			}
		}
		if !version.Effective.Before(applicableEnd) || !version.Effective.Before(end) {
			continue
		}
		day := version.Effective
		if day.Before(start) {
			day = firstDay
		}
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, start.Location())
		for day.Before(applicableEnd) {
			nextDay := day.AddDate(0, 0, 1)
			for dayType := Weekday; dayType <= Holiday; dayType++ {
				for _, period := range version.Schedule[dayType] {
					at := day.Add(time.Duration(period.StartMinute) * time.Minute)
					if at.After(start) && at.Before(end) && !at.Before(version.Effective) {
						boundaries = append(boundaries, at)
					}
				}
			}
			day = nextDay
		}
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i].Before(boundaries[j]) })
	boundaries = compactTimes(boundaries)

	wholeDuration := end.Sub(start)
	pieces := make([]Piece, 0, len(boundaries)-1)
	allocated := Energy(0)
	for index := 0; index+1 < len(boundaries); index++ {
		pieceStart := boundaries[index]
		pieceEnd := boundaries[index+1]
		duration := pieceEnd.Sub(pieceStart)
		energy := new(big.Int).Mul(big.NewInt(total), big.NewInt(duration.Nanoseconds()))
		energy.Div(energy, big.NewInt(wholeDuration.Nanoseconds()))
		if !energy.IsInt64() || energy.Int64() < 0 || energy.Int64() > total {
			return nil, errInvalid("计价结果越界")
		}
		pieceEnergy := energy.Int64()
		if index == len(boundaries)-2 {
			pieceEnergy = total - allocated
			energy.SetInt64(pieceEnergy)
		}
		allocated += pieceEnergy
		piece := Piece{Start: pieceStart, End: pieceEnd, Energy: energy.Int64()}
		piece.DayType = cal.dayType(pieceStart)
		version, ok := tariffs.versionAt(pieceStart)
		if ok {
			piece.Rate = periodRate(version.Schedule, piece.DayType, pieceStart)
			amount := new(big.Int).Mul(big.NewInt(piece.Energy), big.NewInt(int64(piece.Rate)))
			if !amount.IsInt64() {
				return nil, errInvalid("计价结果越界")
			}
			piece.Amount = amount.Int64()
			piece.Priceable = true
		}
		pieces = append(pieces, piece)
	}
	return pieces, nil
}

func compactTimes(values []time.Time) []time.Time {
	if len(values) == 0 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if !value.Equal(result[len(result)-1]) {
			result = append(result, value)
		}
	}
	return result
}

func aggregatePieces(pieces []Piece) Bill {
	order := make([]Rate, 0)
	lines := make(map[Rate]*BillLine)
	bill := Bill{}
	for _, piece := range pieces {
		if !piece.Priceable {
			bill.UnpriceableEnergy += piece.Energy
			bill.UnpriceablePieces++
			continue
		}
		k := piece.Rate
		line, ok := lines[k]
		if !ok {
			billLine := BillLine{Rate: piece.Rate}
			line = &billLine
			order = append(order, k)
			lines[k] = line
		}
		line.Energy += piece.Energy
		line.Amount += piece.Amount
	}
	for _, key := range order {
		bill.Lines = append(bill.Lines, *lines[key])
	}
	sort.Slice(bill.Lines, func(i, j int) bool {
		return bill.Lines[i].Rate < bill.Lines[j].Rate
	})
	return bill
}
