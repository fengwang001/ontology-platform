package parking

import "sort"

type waitEntry struct {
	id         string
	vehicle    string
	start      int64
	end        int64
	needCharge bool
	createdAt  int64
}

func (l *Lot) chooseSpot(z *zone, start, end int64, needCharge bool) (int, bool) {
	if !needCharge {
		if number, ok := l.firstFreeOfKind(z, NormalSpot, start, end); ok {
			return number, true
		}
		if number, ok := l.firstFreeOfKind(z, ChargingSpot, start, end); ok {
			return number, true
		}
		return 0, false
	}
	return l.firstFreeOfKind(z, ChargingSpot, start, end)
}

func (l *Lot) firstFreeOfKind(z *zone, kind SpotKind, start, end int64) (int, bool) {
	for _, number := range z.order {
		if z.spots[number].Kind == kind && l.rangeFree(z.spots[number].Zone, number, start, end) {
			return number, true
		}
	}
	return 0, false
}

func (l *Lot) joinWaitlist(z *zone, entry waitEntry) {
	item := entry
	position := sort.Search(len(z.wait), func(index int) bool {
		if z.wait[index].createdAt != item.createdAt {
			return z.wait[index].createdAt > item.createdAt
		}
		return z.wait[index].vehicle > item.vehicle
	})
	z.wait = append(z.wait, nil)
	copy(z.wait[position+1:], z.wait[position:])
	z.wait[position] = &item
}

func (l *Lot) promoteAll(at int64) {
	for _, zoneName := range l.sortedZoneNames() {
		z := l.zones[zoneName]
		for index := 0; index < len(z.wait); index++ {
			entry := z.wait[index]
			start := entry.start
			if start < at {
				start = at
			}
			number, ok := l.chooseSpot(z, start, entry.end, entry.needCharge)
			if !ok {
				continue
			}
			z.wait = append(z.wait[:index], z.wait[index+1:]...)
			index--
			res := &Reservation{
				ID:         entry.id,
				Zone:       zoneName,
				Vehicle:    entry.vehicle,
				SpotNumber: number,
				Start:      start,
				End:        entry.end,
				NeedCharge: entry.needCharge,
				Status:     StatusReserved,
				CreatedAt:  at,
			}
			l.reservations[res.ID] = res
			l.activeRes[res.ID] = res
			l.assignOwner(zoneName, number, res.Start, res.End, l.ownerOf(res))
		}
	}
}

func (l *Lot) releaseScheduled(res *Reservation) {
	l.assignOwner(res.Zone, res.SpotNumber, res.Start, res.End, Owner{})
}

func (l *Lot) ownerOf(res *Reservation) Owner {
	return Owner{ReservationID: res.ID, Vehicle: res.Vehicle}
}
