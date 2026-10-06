package parking

type intervalMinutes struct {
	start int
	end   int
}

func ceilDiv(value, unit int) int {
	return (value + unit - 1) / unit
}

func calcFee(cfg Config, entryTime, exitTime int, freeEviction int, overtime []intervalMinutes) int64 {
	if exitTime <= entryTime {
		return 0
	}
	normalMinutes := exitTime - entryTime - freeEviction
	for _, part := range overtime {
		normalMinutes -= part.end - part.start
	}
	if normalMinutes < 0 {
		normalMinutes = 0
	}
	if normalMinutes <= cfg.FreeMinutes {
		normalMinutes = 0
	} else {
		normalMinutes -= cfg.FreeMinutes
	}
	var fee int64
	for day := entryTime / minutesPerDay; day <= exitTime/minutesPerDay; day++ {
		dayStart := day * minutesPerDay
		dayEnd := dayStart + minutesPerDay
		left := maxInt(entryTime, dayStart)
		right := minInt(exitTime, dayEnd)
		if right <= left {
			continue
		}
		dayMinutes := right - left
		used := minInt(normalMinutes, dayMinutes)
		normalMinutes -= used
		paid := ceilDiv(used, cfg.BillingUnit)
		dayFee := int64(paid) * cfg.UnitFee
		if dayFee > cfg.DailyCap {
			dayFee = cfg.DailyCap
		}
		fee += dayFee
	}

	for _, part := range overtime {
		fee += int64(part.end-part.start) * cfg.OvertimeFee
	}
	return fee
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
