package approval

import "slices"

type Calendar struct {
	nonWork []int
}

func NewCalendar(nonWorkdays []int) Calendar {
	unique := make(map[int]struct{}, len(nonWorkdays))
	for _, day := range nonWorkdays {
		unique[day] = struct{}{}
	}
	days := make([]int, 0, len(unique))
	for day := range unique {
		days = append(days, day)
	}
	slices.Sort(days)
	return Calendar{nonWork: days}
}

func (c Calendar) IsWorkday(day int) bool {
	_, found := slices.BinarySearch(c.nonWork, day)
	return !found
}

func (c Calendar) NextWorkday(day int) int {
	return c.NthWorkdayOnOrAfter(day+1, 1)
}

func (c Calendar) NthWorkdayFrom(nextDay int, count int) int {
	if count <= 0 {
		return nextDay
	}
	return c.NthWorkdayOnOrAfter(nextDay, count)
}

func (c Calendar) NthWorkdayOnOrAfter(day int, count int) int {
	if count <= 0 {
		return day
	}
	current := day
	if !c.IsWorkday(current) {
		current = c.firstAfter(current)
	}
	count--
	if count == 0 {
		return current
	}

	span := 1
	for c.WorkdaysBetweenInclusive(current+1, current+span) < count {
		span *= 2
	}
	low, high := current+1, current+span
	for low < high {
		middle := low + (high-low)/2
		if c.WorkdaysBetweenInclusive(current+1, middle) >= count {
			high = middle
		} else {
			low = middle + 1
		}
	}
	return low
}

func (c Calendar) WorkdaysBetweenInclusive(start, end int) int {
	if end < start {
		return 0
	}
	return end - start + 1 - c.holidaysBetween(start, end)
}

func (c Calendar) firstAfter(day int) int {
	next := day + 1
	for span := 1; ; span *= 2 {
		if c.WorkdaysBetweenInclusive(next, next+span) > 0 {
			low, high := next, next+span
			for low < high {
				middle := low + (high-low)/2
				if c.WorkdaysBetweenInclusive(next, middle) > 0 {
					high = middle
				} else {
					low = middle + 1
				}
			}
			return low
		}
	}
}

func (c Calendar) holidaysBetween(start, end int) int {
	return c.countOnOrBefore(end) - c.countOnOrBefore(start-1)
}

func (c Calendar) countOnOrBefore(day int) int {
	count, _ := slices.BinarySearchFunc(c.nonWork, day+1, func(holiday, limit int) int {
		if holiday < limit {
			return -1
		}
		return 1
	})
	return count
}
