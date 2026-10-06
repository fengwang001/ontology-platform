package certselector

import (
	"container/heap"
	"sort"
)

type certificateRecord struct {
	certificate Certificate
	exactNames  []string
	wildcards   []string
}

type scheduleGroup struct {
	certificates []*certificateRecord
	boundaries   []int64
	bestEC       []*certificateRecord
	bestRSA      []*certificateRecord
}

type nameIndex struct {
	exact     map[string]*scheduleGroup
	wildcards map[string]*scheduleGroup
}

type scheduleEvent struct {
	time   int64
	start  bool
	record *certificateRecord
}

func newScheduleGroup(certificates []*certificateRecord) *scheduleGroup {
	if len(certificates) == 0 {
		return nil
	}

	boundarySet := make(map[int64]struct{}, len(certificates)*2)
	events := make([]scheduleEvent, 0, len(certificates)*2)
	for _, record := range certificates {
		certificate := record.certificate
		boundarySet[certificate.NotBefore] = struct{}{}
		boundarySet[certificate.NotAfter] = struct{}{}
		events = append(events,
			scheduleEvent{time: certificate.NotBefore, start: true, record: record},
			scheduleEvent{time: certificate.NotAfter, start: false, record: record},
		)
	}

	boundaries := make([]int64, 0, len(boundarySet))
	for boundary := range boundarySet {
		boundaries = append(boundaries, boundary)
	}
	sort.Slice(boundaries, func(i, j int) bool { return boundaries[i] < boundaries[j] })
	sort.Slice(events, func(i, j int) bool {
		if events[i].time != events[j].time {
			return events[i].time < events[j].time
		}
		if events[i].start != events[j].start {
			return !events[i].start
		}
		return events[i].record.certificate.ID < events[j].record.certificate.ID
	})

	activeEC := newCertificateHeap()
	activeRSA := newCertificateHeap()
	bestEC := make([]*certificateRecord, len(boundaries)-1)
	bestRSA := make([]*certificateRecord, len(boundaries)-1)
	eventIndex := 0

	for segmentIndex, boundary := range boundaries[:len(boundaries)-1] {
		for eventIndex < len(events) && events[eventIndex].time == boundary {
			event := events[eventIndex]
			if event.start {
				if event.record.certificate.KeyType == KeyTypeEC {
					heap.Push(activeEC, event.record)
				} else {
					heap.Push(activeRSA, event.record)
				}
			} else if event.record.certificate.KeyType == KeyTypeEC {
				activeEC.remove(event.record)
			} else {
				activeRSA.remove(event.record)
			}
			eventIndex++
		}
		if activeEC.Len() > 0 {
			bestEC[segmentIndex] = activeEC.peek()
		}
		if activeRSA.Len() > 0 {
			bestRSA[segmentIndex] = activeRSA.peek()
		}
	}

	return &scheduleGroup{
		certificates: append([]*certificateRecord(nil), certificates...),
		boundaries:   boundaries,
		bestEC:       bestEC,
		bestRSA:      bestRSA,
	}
}

func (g *scheduleGroup) bestAt(now int64, keyType KeyType) *certificateRecord {
	if len(g.boundaries) == 0 {
		return nil
	}
	index := sort.Search(len(g.boundaries), func(i int) bool { return g.boundaries[i] > now }) - 1
	if index < 0 || index >= len(g.boundaries)-1 {
		return nil
	}
	if keyType == KeyTypeEC {
		return g.bestEC[index]
	}
	return g.bestRSA[index]
}

func betterCertificate(current *certificateRecord, candidate *certificateRecord) *certificateRecord {
	if current == nil {
		return candidate
	}
	if candidate == nil {
		return current
	}

	left := current.certificate
	right := candidate.certificate
	if left.KeyType != right.KeyType {
		if left.KeyType == KeyTypeEC {
			return current
		}
		return candidate
	}
	if left.NotAfter != right.NotAfter {
		if left.NotAfter > right.NotAfter {
			return current
		}
		return candidate
	}
	if left.ID <= right.ID {
		return current
	}
	return candidate
}

func rebuildGroup(groupMap map[string]*scheduleGroup, name string) {
	group := groupMap[name]
	if group == nil || len(group.certificates) == 0 {
		delete(groupMap, name)
		return
	}
	groupMap[name] = newScheduleGroup(group.certificates)
}

type certificateHeap struct {
	records   []*certificateRecord
	positions map[*certificateRecord]int
}

func newCertificateHeap() *certificateHeap {
	return &certificateHeap{positions: make(map[*certificateRecord]int)}
}

func (h *certificateHeap) Len() int { return len(h.records) }

func (h *certificateHeap) Less(i, j int) bool {
	return betterCertificate(h.records[i], h.records[j]) == h.records[i]
}

func (h *certificateHeap) Swap(i, j int) {
	h.records[i], h.records[j] = h.records[j], h.records[i]
	h.positions[h.records[i]] = i
	h.positions[h.records[j]] = j
}

func (h *certificateHeap) Push(value any) {
	record := value.(*certificateRecord)
	h.positions[record] = len(h.records)
	h.records = append(h.records, record)
}

func (h *certificateHeap) Pop() any {
	record := h.records[len(h.records)-1]
	h.records = h.records[:len(h.records)-1]
	h.positions[record] = -1
	return record
}

func (h *certificateHeap) peek() *certificateRecord {
	return h.records[0]
}

func (h *certificateHeap) remove(record *certificateRecord) {
	position, exists := h.positions[record]
	if !exists || position < 0 {
		return
	}
	heap.Remove(h, position)
}
