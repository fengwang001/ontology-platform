package pricing

import (
	"sort"
	"sync"
	"time"
)

type Engine struct {
	mu       sync.Mutex
	loc      *time.Location
	points   map[string]*supplyPoint
	tariffs  tariffBook
	calendar calendar
}

func NewEngine(loc *time.Location) *Engine {
	if loc == nil {
		loc = time.UTC
	}
	return &Engine{
		loc:      loc,
		points:   make(map[string]*supplyPoint),
		tariffs:  tariffBook{},
		calendar: newCalendar(loc),
	}
}

func (e *Engine) RegisterTariff(version TariffVersion) error {
	version.Effective = normalizeTime(version.Effective, e.loc)
	if err := e.tariffs.validateInsert(version); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rejectIfGloballyClosed(version.Effective); err != nil {
		return err
	}
	index, err := e.tariffs.insertAt(version)
	if err != nil {
		return err
	}
	if err := e.validateTariffRange(index); err != nil {
		e.tariffs.versions = e.tariffs.versions[:index+copy(e.tariffs.versions[index:], e.tariffs.versions[index+1:])]
		return err
	}
	if err := e.recalculateTariffRange(index); err != nil {
		return err
	}
	return nil
}

func (e *Engine) SetHoliday(date string, holiday bool) error {
	day, err := parseHolidayDate(date, e.loc)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rejectIfGloballyClosed(day); err != nil {
		return err
	}
	before := e.calendar.hasHoliday(date)
	if err := e.calendar.setHoliday(date, holiday); err != nil {
		return err
	}
	if err := e.validateDate(day); err != nil {
		if before {
			_ = e.calendar.setHoliday(date, true)
		} else {
			_ = e.calendar.setHoliday(date, false)
		}
		return err
	}
	if err := e.recalculateDate(day); err != nil {
		if before {
			_ = e.calendar.setHoliday(date, true)
		} else {
			_ = e.calendar.setHoliday(date, false)
		}
		return err
	}
	return nil
}

func (e *Engine) RegisterReading(pointName string, at time.Time, total int64) error {
	if err := validateReadingInput(pointName, at, total); err != nil {
		return err
	}
	at = normalizeTime(at, e.loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	point := e.point(pointName)
	if latest, ok := point.latestReading(); ok && point.intervalTouchesClosed(latest.At, at) {
		return errClosed
	}
	if latest, ok := point.latestReading(); ok && !at.After(latest.At) {
		return errOrder
	}
	if latest, ok := point.latestReading(); ok && total < latest.Total {
		return errRewind
	}
	var pieces []Piece
	if latest, ok := point.latestReading(); ok {
		var err error
		pieces, err = sliceInterval(latest.At, at, total-latest.Total, &e.tariffs, &e.calendar)
		if err != nil {
			return err
		}
	}
	point.appendReading(at, total)
	if len(point.intervals) > 0 {
		index := len(point.intervals) - 1
		point.intervals[index].pieces = pieces
		point.addPieces(pieces)
	}
	return nil
}

func (e *Engine) CorrectReading(pointName string, at time.Time, total int64) error {
	if err := validateReadingInput(pointName, at, total); err != nil {
		return err
	}
	at = normalizeTime(at, e.loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	point := e.point(pointName)
	index := point.readingIndex(at)
	if index >= len(point.readings) || !point.readings[index].At.Equal(at) {
		return errOrder
	}
	if point.isClosedMonth(monthKey(at)) {
		return errClosed
	}
	affected := make([]int, 0, 2)
	if index > 0 {
		affected = append(affected, index-1)
	}
	if index+1 < len(point.readings) {
		affected = append(affected, index)
	}
	if e.intervalsTouchClosed(point, affected) {
		return errClosed
	}
	if index > 0 && total < point.readings[index-1].Total {
		return errRewind
	}
	if index+1 < len(point.readings) && total > point.readings[index+1].Total {
		return errRewind
	}
	oldTotal := point.readings[index].Total
	point.readings[index].Total = total
	if index > 0 {
		point.intervals[index-1].to.Total = total
	}
	if index < len(point.intervals) {
		point.intervals[index].from.Total = total
	}
	if err := e.recalculateIntervals(point, affected); err != nil {
		point.readings[index].Total = oldTotal
		if index > 0 {
			point.intervals[index-1].to.Total = oldTotal
		}
		if index < len(point.intervals) {
			point.intervals[index].from.Total = oldTotal
		}
		return err
	}
	return nil
}

func (e *Engine) DeleteReading(pointName string, at time.Time) error {
	if err := validatePointAndTime(pointName, at); err != nil {
		return err
	}
	at = normalizeTime(at, e.loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	point := e.point(pointName)
	index := point.readingIndex(at)
	if index >= len(point.readings) || !point.readings[index].At.Equal(at) {
		return errOrder
	}
	if point.isClosedMonth(monthKey(at)) {
		return errClosed
	}
	affected := make([]int, 0, 2)
	if index > 0 {
		affected = append(affected, index-1)
	}
	if index+1 < len(point.readings) {
		affected = append(affected, index)
	}
	if e.intervalsTouchClosed(point, affected) {
		return errClosed
	}
	if len(affected) == 2 {
		merged := interval{
			from: point.readings[index-1],
			to:   point.readings[index+1],
		}
		pieces, err := sliceInterval(merged.from.At, merged.to.At, merged.to.Total-merged.from.Total, &e.tariffs, &e.calendar)
		if err != nil {
			return err
		}
		merged.pieces = pieces
		point.removePieces(point.intervals[index-1].pieces)
		point.removePieces(point.intervals[index].pieces)
		point.intervals[index-1] = merged
		point.addPieces(merged.pieces)
		point.intervals = append(point.intervals[:index], point.intervals[index+1:]...)
	} else if len(affected) == 1 {
		intervalIndex := affected[0]
		point.removePieces(point.intervals[intervalIndex].pieces)
		point.intervals = append(point.intervals[:intervalIndex], point.intervals[intervalIndex+1:]...)
	}
	point.readings = append(point.readings[:index], point.readings[index+1:]...)
	return nil
}

func (e *Engine) Bill(pointName string, at time.Time) (Bill, error) {
	if err := validatePointAndTime(pointName, at); err != nil {
		return Bill{}, err
	}
	at = normalizeTime(at, e.loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	point := e.point(pointName)
	bill := point.billForUpdate(monthKey(at))
	result := cloneBill(*bill)
	sort.Slice(result.Lines, func(i, j int) bool { return result.Lines[i].Rate < result.Lines[j].Rate })
	return result, nil
}

func (e *Engine) CloseMonth(pointName string, at time.Time) error {
	if err := validatePointAndTime(pointName, at); err != nil {
		return err
	}
	at = normalizeTime(at, e.loc)
	e.mu.Lock()
	defer e.mu.Unlock()
	point := e.point(pointName)
	key := monthKey(at)
	if point.isClosedMonth(key) {
		return nil
	}
	bill := point.billForUpdate(key)
	if bill.UnpriceablePieces > 0 {
		return errShortage
	}
	_, monthEnd := monthRange(key, e.loc)
	index := point.readingIndex(monthEnd)
	if index >= len(point.readings) {
		return errShortage
	}
	point.markClosed(key)
	return nil
}

func (b *tariffBook) validateInsert(version TariffVersion) error {
	return b.insertCheck(version)
}

func (b *tariffBook) insertAt(version TariffVersion) (int, error) {
	if err := b.insertCheck(version); err != nil {
		return -1, err
	}
	index := sort.Search(len(b.versions), func(i int) bool {
		return b.versions[i].Effective.After(version.Effective)
	})
	b.versions = append(b.versions[:index], append([]TariffVersion{version}, b.versions[index:]...)...)
	return index, nil
}

func (b *tariffBook) insertCheck(version TariffVersion) error {
	if version.Effective.IsZero() {
		return errInvalid("生效时刻越界")
	}
	if err := validateSchedule(version.Schedule); err != nil {
		return err
	}
	for _, existing := range b.versions {
		if existing.Effective.Equal(version.Effective) {
			return errInvalid("版本生效时刻重复")
		}
	}
	return nil
}

func normalizeTime(at time.Time, loc *time.Location) time.Time {
	if at.IsZero() {
		return at
	}
	return at.In(loc)
}

func validateReadingInput(pointName string, at time.Time, total int64) error {
	if err := validatePointAndTime(pointName, at); err != nil {
		return err
	}
	if total < 0 {
		return errInvalid("电量不能为负")
	}
	return nil
}

func validatePointAndTime(pointName string, at time.Time) error {
	if pointName == "" {
		return errInvalid("供电点不能为空")
	}
	if at.IsZero() {
		return errInvalid("时刻越界")
	}
	return nil
}

func (e *Engine) point(name string) *supplyPoint {
	point, ok := e.points[name]
	if !ok {
		point = newSupplyPoint(name)
		e.points[name] = point
	}
	return point
}

func (e *Engine) rejectIfGloballyClosed(at time.Time) error {
	for _, point := range e.points {
		index := sort.SearchInts(point.closedKeys, monthKey(at))
		if index < len(point.closedKeys) {
			return errClosed
		}
	}
	return nil
}

func (e *Engine) intervalsTouchClosed(point *supplyPoint, indexes []int) bool {
	for _, index := range indexes {
		if point.intervalTouchesClosed(point.intervals[index].from.At, point.intervals[index].to.At) {
			return true
		}
	}
	return false
}

func (e *Engine) recalculateIntervals(point *supplyPoint, indexes []int) error {
	for _, index := range indexes {
		item := point.intervals[index]
		pieces, err := sliceInterval(item.from.At, item.to.At, item.to.Total-item.from.Total, &e.tariffs, &e.calendar)
		if err != nil {
			return err
		}
		point.replaceIntervalPieces(index, pieces)
	}
	return nil
}

func (e *Engine) validateIntervals(point *supplyPoint, indexes []int, tariffs *tariffBook, cal *calendar) error {
	for _, index := range indexes {
		item := point.intervals[index]
		if _, err := sliceInterval(item.from.At, item.to.At, item.to.Total-item.from.Total, tariffs, cal); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) validateTariffRange(versionIndex int) error {
	version := e.tariffs.versions[versionIndex]
	nextEffective := time.Time{}
	if versionIndex+1 < len(e.tariffs.versions) {
		nextEffective = e.tariffs.versions[versionIndex+1].Effective
	}
	for _, point := range e.points {
		indexes := make([]int, 0)
		for index, item := range point.intervals {
			if point.isClosedMonth(monthKey(item.from.At)) || point.isClosedMonth(monthKey(item.to.At)) {
				continue
			}
			if !item.to.At.After(version.Effective) {
				continue
			}
			if !nextEffective.IsZero() && !item.from.At.Before(nextEffective) {
				continue
			}
			indexes = append(indexes, index)
		}
		if err := e.validateIntervals(point, indexes, &e.tariffs, &e.calendar); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) validateDate(day time.Time) error {
	nextDay := day.AddDate(0, 0, 1)
	for _, point := range e.points {
		indexes := make([]int, 0)
		for index, item := range point.intervals {
			if point.isClosedMonth(monthKey(item.from.At)) || point.isClosedMonth(monthKey(item.to.At)) {
				continue
			}
			if item.to.At.After(day) && item.from.At.Before(nextDay) {
				indexes = append(indexes, index)
			}
		}
		if err := e.validateIntervals(point, indexes, &e.tariffs, &e.calendar); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) recalculateTariffRange(versionIndex int) error {
	version := e.tariffs.versions[versionIndex]
	nextEffective := time.Time{}
	if versionIndex+1 < len(e.tariffs.versions) {
		nextEffective = e.tariffs.versions[versionIndex+1].Effective
	}
	for _, point := range e.points {
		for index, item := range point.intervals {
			if point.isClosedMonth(monthKey(item.from.At)) || point.isClosedMonth(monthKey(item.to.At)) {
				continue
			}
			if !item.to.At.After(version.Effective) {
				continue
			}
			if !nextEffective.IsZero() && !item.from.At.Before(nextEffective) {
				continue
			}
			if err := e.recalculateIntervals(point, []int{index}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e *Engine) recalculateDate(day time.Time) error {
	nextDay := day.AddDate(0, 0, 1)
	for _, point := range e.points {
		for index, item := range point.intervals {
			if point.isClosedMonth(monthKey(item.from.At)) || point.isClosedMonth(monthKey(item.to.At)) {
				continue
			}
			if item.to.At.After(day) && item.from.At.Before(nextDay) {
				if err := e.recalculateIntervals(point, []int{index}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
