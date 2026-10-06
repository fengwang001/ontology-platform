package ontology

import "container/heap"

type endItem struct {
	kind string
	end  int64
	id   string
	ver  int
}

const (
	endBooking = "booking"
	endPermit  = "permit"
)

type endHeap []endItem

func (h endHeap) Len() int            { return len(h) }
func (h endHeap) Less(i, j int) bool  { return h[i].end < h[j].end }
func (h endHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *endHeap) Push(x interface{}) { *h = append(*h, x.(endItem)) }
func (h *endHeap) Pop() interface{} {
	old := *h
	last := len(old) - 1
	item := old[last]
	*h = old[:last]
	return item
}

func (s *Service) advanceClock(now int64) {
	for s.bookingEnds.Len() > 0 && s.bookingEnds[0].end < now {
		item := heap.Pop(&s.bookingEnds).(endItem)
		if item.kind == endBooking {
			if record := s.bookings[item.id]; record != nil && record.Status == BookingReserved {
				record.Status = BookingNoShow
				record.Penalties++
				s.chargePenalty(record.Household, record.ID, item.end)
			}
		}
	}
	today := now / MinutesPerDay
	for s.permitEnds.Len() > 0 && s.permitEnds[0].end <= today {
		item := heap.Pop(&s.permitEnds).(endItem)
		if item.kind == endPermit {
			if record := s.permits[item.id]; record != nil && record.heapVersion == item.ver &&
				(record.Status == PermitApplied || record.Status == PermitApproved ||
					record.Status == PermitActive || record.Status == PermitSuspended) {
				if record.Status == PermitApplied {
					s.releaseHold(record.Household)
				}
				record.Status = PermitExpired
				record.CheckedIn = false
			}
		}
	}
	s.commitClock(now)
}
