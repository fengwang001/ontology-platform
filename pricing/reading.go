package pricing

import (
	"sort"
	"time"
)

type Reading struct {
	At    time.Time
	Total int64
}

type supplyPoint struct {
	name       string
	readings   []Reading
	intervals  []interval
	bills      map[int]*Bill
	closed     map[int]struct{}
	closedKeys []int
}

type interval struct {
	from   Reading
	to     Reading
	pieces []Piece
}

func newSupplyPoint(name string) *supplyPoint {
	return &supplyPoint{name: name, bills: make(map[int]*Bill), closed: make(map[int]struct{})}
}

func (p *supplyPoint) latestReading() (Reading, bool) {
	if len(p.readings) == 0 {
		return Reading{}, false
	}
	return p.readings[len(p.readings)-1], true
}

func (p *supplyPoint) readingIndex(at time.Time) int {
	return sort.Search(len(p.readings), func(i int) bool {
		return !p.readings[i].At.Before(at)
	})
}

func (p *supplyPoint) isClosedMonth(key int) bool {
	_, ok := p.closed[key]
	return ok
}

func (p *supplyPoint) intervalTouchesClosed(start, end time.Time) bool {
	if start.After(end) {
		start, end = end, start
	}
	startKey := monthKey(start)
	endKey := monthKey(end)
	index := sort.SearchInts(p.closedKeys, startKey)
	return index < len(p.closedKeys) && p.closedKeys[index] <= endKey
}

func (p *supplyPoint) markClosed(key int) {
	if p.isClosedMonth(key) {
		return
	}
	p.closed[key] = struct{}{}
	p.closedKeys = append(p.closedKeys, key)
	sort.Ints(p.closedKeys)
}

func (p *supplyPoint) appendReading(at time.Time, total int64) {
	if latest, ok := p.latestReading(); ok {
		p.intervals = append(p.intervals, interval{from: latest, to: Reading{At: at, Total: total}})
	}
	p.readings = append(p.readings, Reading{At: at, Total: total})
}

func (p *supplyPoint) replaceIntervalPieces(index int, pieces []Piece) {
	old := p.intervals[index].pieces
	p.removePieces(old)
	p.intervals[index].pieces = pieces
	p.addPieces(pieces)
}

func (p *supplyPoint) addPieces(pieces []Piece) {
	for _, piece := range pieces {
		p.addPiece(monthKey(piece.Start), piece)
	}
}

func (p *supplyPoint) removePieces(pieces []Piece) {
	for _, piece := range pieces {
		p.removePiece(monthKey(piece.Start), piece)
	}
}

func (p *supplyPoint) addPiece(key int, piece Piece) {
	bill := p.billForUpdate(key)
	if !piece.Priceable {
		bill.UnpriceableEnergy += piece.Energy
		bill.UnpriceablePieces++
		return
	}
	for index := range bill.Lines {
		if bill.Lines[index].Rate == piece.Rate {
			bill.Lines[index].Energy += piece.Energy
			bill.Lines[index].Amount += piece.Amount
			return
		}
	}
	bill.Lines = append(bill.Lines, BillLine{Rate: piece.Rate, Energy: piece.Energy, Amount: piece.Amount})
}

func (p *supplyPoint) removePiece(key int, piece Piece) {
	bill := p.billForUpdate(key)
	if !piece.Priceable {
		bill.UnpriceableEnergy -= piece.Energy
		bill.UnpriceablePieces--
		return
	}
	for index := range bill.Lines {
		if bill.Lines[index].Rate == piece.Rate {
			bill.Lines[index].Energy -= piece.Energy
			bill.Lines[index].Amount -= piece.Amount
			if bill.Lines[index].Energy == 0 {
				bill.Lines = append(bill.Lines[:index], bill.Lines[index+1:]...)
			}
			return
		}
	}
}

func (p *supplyPoint) billForUpdate(key int) *Bill {
	bill, ok := p.bills[key]
	if !ok {
		bill = &Bill{}
		p.bills[key] = bill
	}
	return bill
}

func cloneBill(bill Bill) Bill {
	lines := make([]BillLine, 0, len(bill.Lines))
	for _, line := range bill.Lines {
		if line.Energy != 0 {
			lines = append(lines, line)
		}
	}
	bill.Lines = lines
	return bill
}
