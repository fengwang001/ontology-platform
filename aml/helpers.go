package aml

import "math"

func subtractDays(now int64, day int) int64 {
	offset := int64(day)
	if now < math.MinInt64+offset {
		return math.MinInt64
	}
	return now - offset
}

func expireDates(previousLast, currentDate, windowDays int64) []int64 {
	if currentDate <= previousLast {
		return nil
	}
	gap := elapsedDays(previousLast, currentDate)
	if gap >= windowDays {
		return []int64{math.MinInt64}
	}

	lastExpired := subtractDays(currentDate, int(windowDays))
	firstExpired := addDays(subtractDays(previousLast, int(windowDays)), 1)
	count := gap
	dates := make([]int64, 0, count)
	for date := firstExpired; date <= lastExpired; date++ {
		dates = append(dates, date)
		if date == math.MaxInt64 {
			break
		}
	}
	return dates
}

func addDays(date int64, day int) int64 {
	offset := int64(day)
	if date > math.MaxInt64-offset {
		return math.MaxInt64
	}
	return date + offset
}

func elapsedDays(from, to int64) int64 {
	if from < 0 && to > math.MaxInt64+from {
		return math.MaxInt64
	}
	return to - from
}

func saturatingAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

func cloneReport(report Report) Report {
	clone := report
	clone.Accounts = append([]string(nil), report.Accounts...)
	clone.TransactionIDs = append([]string(nil), report.TransactionIDs...)
	return clone
}

func cloneReportPointer(report *Report) *Report {
	clone := cloneReport(*report)
	return &clone
}
