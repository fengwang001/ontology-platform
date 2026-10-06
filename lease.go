package parking

import "sort"

func validTimeRange(start, end int) bool {
	if start < 0 || start >= Day || end < 0 || end > Day || start == end {
		return false
	}
	return start < end || end < start
}

func validLeaseRange(start, end int) bool {
	return start >= 0 && end > start
}

func dayStart(t int) int {
	return (t / Day) * Day
}

func leaseActive(lease *Lease, now int) bool {
	return now >= lease.Start && now < lease.End
}

func canRenew(lease *Lease, now int) bool {
	return now >= lease.Start && now < lease.GraceTo
}

func intervalAt(interval Interval, day int) (int, int) {
	start := dayStart(day) + interval.Start
	end := dayStart(day) + interval.End
	if interval.End <= interval.Start {
		end += Day
	}
	return start, end
}

func intervalOpen(lease *Lease, now int) bool {
	if !lease.HasShare {
		return false
	}
	day := dayStart(now)
	start, end := intervalAt(lease.Share, day)
	if lease.Share.End > lease.Share.Start {
		return now >= start && now < end
	}
	if now >= start && now < end {
		return true
	}
	previous := day - Day
	prevStart, prevEnd := intervalAt(lease.Share, previous)
	return now >= prevStart && now < prevEnd
}

func nextIntervalBound(lease *Lease, now int) (int, bool) {
	if !lease.HasShare {
		return 0, false
	}
	currentDay := dayStart(now)
	bounds := make([]int, 0, 4)
	for dayOffset := -1; dayOffset <= 1; dayOffset++ {
		day := currentDay + dayOffset*Day
		start, end := intervalAt(lease.Share, day)
		bounds = append(bounds, start, end)
	}
	sort.Ints(bounds)
	for _, bound := range bounds {
		if bound > now {
			return bound, true
		}
	}
	return 0, false
}

func intervalBoundsCovering(lease *Lease, now int) (int, int, bool) {
	if !lease.HasShare {
		return 0, 0, false
	}
	day := dayStart(now)
	start, end := intervalAt(lease.Share, day)
	if now >= start && now < end {
		return start, end, true
	}
	previous := day - Day
	prevStart, prevEnd := intervalAt(lease.Share, previous)
	if now >= prevStart && now < prevEnd {
		return prevStart, prevEnd, true
	}
	return 0, 0, false
}
