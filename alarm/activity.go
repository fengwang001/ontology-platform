package alarm

import "sort"

type activityIndex struct {
	entries     map[string]ActiveAlarm
	appearances []int64
}

func newActivityIndex() *activityIndex {
	return &activityIndex{entries: make(map[string]ActiveAlarm)}
}

func (i *activityIndex) show(entry ActiveAlarm, at int64) {
	if _, exists := i.entries[entry.PointID]; exists {
		i.entries[entry.PointID] = entry
		return
	}
	i.entries[entry.PointID] = entry
	i.appearances = append(i.appearances, at)
}

func (i *activityIndex) update(entry ActiveAlarm) {
	if _, exists := i.entries[entry.PointID]; exists {
		i.entries[entry.PointID] = entry
	}
}

func (i *activityIndex) hide(pointID string) {
	delete(i.entries, pointID)
}

func (i *activityIndex) contains(pointID string) bool {
	_, exists := i.entries[pointID]
	return exists
}

func (i *activityIndex) snapshot() []ActiveAlarm {
	entries := make([]ActiveAlarm, 0, len(i.entries))
	for _, entry := range i.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].Priority != entries[right].Priority {
			return entries[left].Priority < entries[right].Priority
		}
		leftUnacknowledged := entries[left].State == ActiveUnacknowledged || entries[left].State == ReturnedUnacknowledged
		rightUnacknowledged := entries[right].State == ActiveUnacknowledged || entries[right].State == ReturnedUnacknowledged
		if leftUnacknowledged != rightUnacknowledged {
			return leftUnacknowledged
		}
		if entries[left].LastActivation != entries[right].LastActivation {
			return entries[left].LastActivation < entries[right].LastActivation
		}
		return entries[left].PointID < entries[right].PointID
	})
	return entries
}

func (i *activityIndex) rate(at int64, duration int64) int {
	start := at - duration
	first := sort.Search(len(i.appearances), func(index int) bool {
		return i.appearances[index] > start
	})
	return len(i.appearances) - first
}
