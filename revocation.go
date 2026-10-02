package ontology

import "container/heap"

type revocationKey struct {
	id        string
	length    int
	signature string
}

type revocationRecord struct {
	key       revocationKey
	boundary  uint64
	infinite  bool
	sequence  uint64
	heapIndex int
}

type revocationHeap []*revocationRecord

func (h revocationHeap) Len() int { return len(h) }

func (h revocationHeap) Less(i, j int) bool {
	if h[i].boundary != h[j].boundary {
		return h[i].boundary < h[j].boundary
	}
	return h[i].sequence < h[j].sequence
}

func (h revocationHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

func (h *revocationHeap) Push(value any) {
	record := value.(*revocationRecord)
	record.heapIndex = len(*h)
	*h = append(*h, record)
}

func (h *revocationHeap) Pop() any {
	old := *h
	last := len(old) - 1
	record := old[last]
	old[last] = nil
	record.heapIndex = -1
	*h = old[:last]
	return record
}

type revocationTable struct {
	byKey        map[revocationKey]*revocationRecord
	active       revocationHeap
	count        int
	nextSequence uint64
}

func newRevocationTable() revocationTable {
	return revocationTable{byKey: make(map[revocationKey]*revocationRecord)}
}

func (table *revocationTable) expireAt(now uint64) int {
	popped := 0
	for len(table.active) > 0 {
		record := table.active[0]
		if record.boundary > now {
			break
		}
		heap.Pop(&table.active)
		delete(table.byKey, record.key)
		table.count--
		popped++
	}
	return popped
}

func (table *revocationTable) contains(key revocationKey, now uint64) bool {
	record := table.byKey[key]
	if !recordActive(record, now) {
		return false
	}
	return true
}

func (table *revocationTable) add(record *revocationRecord) {
	if existing := table.byKey[record.key]; existing != nil && !existing.infinite {
		heap.Remove(&table.active, existing.heapIndex)
	} else {
		table.count++
	}
	table.byKey[record.key] = record
	if !record.infinite {
		table.nextSequence++
		record.sequence = table.nextSequence
		heap.Push(&table.active, record)
	}
}

func (table *revocationTable) activeCount() int { return table.count }

func recordActive(record *revocationRecord, now uint64) bool {
	return record != nil && (record.infinite || record.boundary > now)
}

var _ heap.Interface = (*revocationHeap)(nil)
