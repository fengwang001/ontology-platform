package railway

// mergeDueOrigins folds every allocation whose cutoff is at or before now
// into the shared quota. Origins are processed in departure order, making
// the irreversible merge deterministic for any operation timestamp.
func (tr *train) mergeDueOrigins(now int64) {
	for tr.mergedBefore < len(tr.stations)-1 {
		origin := tr.mergedBefore
		if tr.cutoff(origin) > now {
			break
		}
		for destination := origin + 1; destination < len(tr.stations); destination++ {
			tr.sharedQuota += tr.quota[origin][destination]
			tr.quota[origin][destination] = 0
		}
		tr.mergedBefore = origin + 1
	}
}

func (tr *train) useQuota(origin, destination int) (usedShared, ok bool) {
	if tr.quota[origin][destination] > 0 {
		tr.quota[origin][destination]--
		tr.sold[origin][destination]++
		return false, true
	}
	if tr.sharedQuota > 0 {
		tr.sharedQuota--
		tr.sold[origin][destination]++
		return true, true
	}
	return false, false
}

func (tr *train) refundQuota(origin, destination int, usedShared bool) {
	tr.sold[origin][destination]--
	if usedShared || origin < tr.mergedBefore {
		tr.sharedQuota++
		return
	}
	tr.quota[origin][destination]++
}

func (tr *train) effectiveAllocation(origin, destination, nowMergedBefore int, now int64) int {
	if origin < nowMergedBefore || tr.cutoff(origin) <= now {
		return 0
	}
	return tr.quota[origin][destination]
}

func (tr *train) sharedAt(nowMergedBefore int, now int64) int {
	shared := tr.sharedQuota
	for origin := nowMergedBefore; origin < len(tr.stations)-1; origin++ {
		if tr.cutoff(origin) > now {
			break
		}
		for destination := origin + 1; destination < len(tr.stations); destination++ {
			shared += tr.quota[origin][destination]
		}
	}
	return shared
}

func (tr *train) standingFree(origin, destination int) bool {
	for edge := origin; edge < destination; edge++ {
		if tr.standUsed[edge] >= tr.standCap {
			return false
		}
	}
	return true
}

func (tr *train) addStanding(origin, destination int) {
	for edge := origin; edge < destination; edge++ {
		tr.standUsed[edge]++
	}
}

func (tr *train) removeStanding(origin, destination int) {
	for edge := origin; edge < destination; edge++ {
		tr.standUsed[edge]--
	}
}
