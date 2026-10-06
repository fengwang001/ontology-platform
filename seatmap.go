package railway

// seatMap stores occupancy only as per-seat, per-edge booleans.
// It never scans tickets, so seat choice is O(seats*stations).
type seatMap struct {
	occupied [][]bool
}

func newSeatMap(seatCount, stationCount int) *seatMap {
	occupied := make([][]bool, seatCount)
	for seatIndex := range occupied {
		occupied[seatIndex] = make([]bool, stationCount-1)
	}
	return &seatMap{occupied: occupied}
}

// choose returns -1 when no seat is free over the whole half-open segment.
// A left touch means another sold interval ends exactly at origin; a right
// touch means one starts exactly at destination. Both are read from boundary
// edges instead of iterating sold tickets.
func (m *seatMap) choose(origin, destination int) int {
	bestSeat := -1
	bestClass := 3
	for seatIndex := range m.occupied {
		if !m.free(seatIndex, origin, destination) {
			continue
		}
		leftTouch := origin > 0 && m.occupied[seatIndex][origin-1]
		rightTouch := destination < len(m.occupied[seatIndex]) && m.occupied[seatIndex][destination]
		class := seatClass(leftTouch, rightTouch)
		if class < bestClass {
			bestClass = class
			bestSeat = seatIndex
		}
	}
	return bestSeat
}

func (m *seatMap) free(seatIndex, origin, destination int) bool {
	for edge := origin; edge < destination; edge++ {
		if m.occupied[seatIndex][edge] {
			return false
		}
	}
	return true
}

func (m *seatMap) mark(seatIndex, origin, destination int, occupied bool) {
	for edge := origin; edge < destination; edge++ {
		m.occupied[seatIndex][edge] = occupied
	}
}

func seatClass(leftTouch, rightTouch bool) int {
	switch {
	case leftTouch && rightTouch:
		return 0
	case leftTouch || rightTouch:
		return 1
	default:
		return 2
	}
}
