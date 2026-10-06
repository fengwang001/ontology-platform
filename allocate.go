package ontology

import "sort"

type occupant struct {
	tenantID int
	roomID   int
	area     int
	order    [2]int
}

func allocateBill(bill Bill, tenants map[int]*Tenant, rooms map[int]Room) []Share {
	days := bill.End - bill.Start
	if days <= 0 || bill.FinalAmount <= 0 {
		return nil
	}
	daily := bill.FinalAmount / days
	extraDays := bill.FinalAmount % days
	shares := make([]Share, 0, days)
	for offset := 0; offset < days; offset++ {
		day := bill.Start + offset
		amount := daily
		if offset < extraDays {
			amount++
		}
		shares = append(shares, allocateDay(bill, day, amount, tenants, rooms)...)
	}
	return shares
}

func allocateDay(bill Bill, day, amount int, tenants map[int]*Tenant, rooms map[int]Room) []Share {
	present := make([]occupant, 0)
	var future *occupant
	futureStart := 0
	for tenantID, tenant := range tenants {
		for index, segment := range tenant.Segments {
			moveIn := segment.Start
			for trace := index; trace > 0 && tenant.Segments[trace-1].End == tenant.Segments[trace].Start; trace-- {
				moveIn = tenant.Segments[trace-1].Start
			}
			room := rooms[segment.RoomID]
			candidate := occupant{
				tenantID: tenantID,
				roomID:   segment.RoomID,
				area:     room.Area,
				order:    [2]int{moveIn, segment.RoomID},
			}
			if day >= segment.Start && day < segment.End {
				present = append(present, candidate)
			}
			if segment.Start >= day && (future == nil || segment.Start < futureStart || (segment.Start == futureStart && segment.RoomID < future.roomID)) {
				start := segment.Start
				future = &candidate
				futureStart = start
			}
		}
	}
	if len(present) == 0 {
		if future == nil {
			return []Share{{Day: day, Amount: amount, Landlord: true}}
		}
		return []Share{{TenantID: future.tenantID, Day: day, Amount: amount}}
	}
	sort.Slice(present, func(i, j int) bool {
		if present[i].order != present[j].order {
			return present[i].order[0] < present[j].order[0] ||
				(present[i].order[0] == present[j].order[0] && present[i].order[1] < present[j].order[1])
		}
		return present[i].tenantID < present[j].tenantID
	})
	assigned := make([]int, len(present))
	if bill.ByArea {
		totalArea := 0
		for _, person := range present {
			totalArea += person.area
		}
		remaining := amount
		for i, person := range present {
			assigned[i] = amount * person.area / totalArea
			remaining -= assigned[i]
		}
		for i := 0; i < remaining; i++ {
			assigned[i%len(assigned)]++
		}
	} else {
		base := amount / len(present)
		for i := range assigned {
			assigned[i] = base
		}
		for i := 0; i < amount%len(present); i++ {
			assigned[i]++
		}
	}
	shares := make([]Share, 0, len(present))
	for i, person := range present {
		if assigned[i] > 0 {
			shares = append(shares, Share{TenantID: person.tenantID, Day: day, Amount: assigned[i]})
		}
	}
	return shares
}
