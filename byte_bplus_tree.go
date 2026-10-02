package ontology

import (
	"errors"
	"slices"
	"sync"
)

var (
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrDuplicateKey     = errors.New("duplicate key")
	ErrKeyNotFound      = errors.New("key not found")
	ErrPageLimit        = errors.New("page limit reached")
)

type Record struct {
	Key  string
	Size int
}

type Page struct {
	ID       int
	Keys     []string
	Occupied int
}

type ByteBPlusTree struct {
	mu        sync.RWMutex
	capacity  int
	pageLimit int
	pages     []*leafPage
	nextPage  int

	comparisonCount uint64
}

func NewByteBPlusTree(capacity int, pageLimit int) (*ByteBPlusTree, error) {
	if capacity < 8 || capacity > 1_000_000 || pageLimit < 1 || pageLimit > 1_000_000 {
		return nil, ErrInvalidParameter
	}

	root := &leafPage{id: 1}
	return &ByteBPlusTree{
		capacity:  capacity,
		pageLimit: pageLimit,
		pages:     []*leafPage{root},
		nextPage:  2,
	}, nil
}

func (t *ByteBPlusTree) Insert(key string, size int) error {
	if key == "" || size < 1 || size > t.maxRecordSize() {
		return ErrInvalidParameter
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	pageIndex := t.locatePageIndex(key)
	page := t.pages[pageIndex]
	recordIndex, exists := page.findRecord(key)
	if exists {
		return ErrDuplicateKey
	}

	if page.occupied+size > t.capacity && len(t.pages) == t.pageLimit {
		return ErrPageLimit
	}

	page.records = slices.Insert(page.records, recordIndex, record{key: key, size: size})
	page.occupied += size

	if page.occupied <= t.capacity {
		return nil
	}

	t.splitPage(pageIndex)
	return nil
}

func (t *ByteBPlusTree) Delete(key string) error {
	if key == "" {
		return ErrInvalidParameter
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	pageIndex := t.locatePageIndex(key)
	page := t.pages[pageIndex]
	recordIndex, found := page.findRecord(key)
	if !found {
		return ErrKeyNotFound
	}

	page.occupied -= page.records[recordIndex].size
	page.records = slices.Delete(page.records, recordIndex, recordIndex+1)

	if len(t.pages) == 1 || page.occupied >= t.minimumOccupancy() {
		return nil
	}

	t.rebalance(pageIndex)
	return nil
}

func (t *ByteBPlusTree) Pages() []Page {
	t.mu.RLock()
	defer t.mu.RUnlock()

	pages := make([]Page, len(t.pages))
	for i, page := range t.pages {
		keys := make([]string, len(page.records))
		for j, item := range page.records {
			keys[j] = item.key
		}
		pages[i] = Page{
			ID:       page.id,
			Keys:     keys,
			Occupied: page.occupied,
		}
	}
	return pages
}

type record struct {
	key  string
	size int
}

type leafPage struct {
	id       int
	records  []record
	occupied int
}

func (t *ByteBPlusTree) maxRecordSize() int {
	return t.capacity / 4
}

func (t *ByteBPlusTree) locatePageIndex(key string) int {
	left := 0
	right := len(t.pages)
	for right-left > 1 {
		middle := left + (right-left)/2
		t.comparisonCount++
		if t.pages[middle].records[0].key <= key {
			left = middle
		} else {
			right = middle
		}
	}
	return left
}

func (p *leafPage) findRecord(key string) (int, bool) {
	index, found := slices.BinarySearchFunc(p.records, key, func(item record, target string) int {
		if item.key < target {
			return -1
		}
		if item.key > target {
			return 1
		}
		return 0
	})
	if !found {
		return index, false
	}
	return index, true
}

func (t *ByteBPlusTree) splitPage(pageIndex int) {
	page := t.pages[pageIndex]
	total := page.occupied
	bestPosition := 1
	bestDifference := total + 1
	bestLeftTotal := 0

	for position := 1; position < len(page.records); position++ {
		leftTotal := leftSum(page.records[:position])
		difference := leftTotal - (total - leftTotal)
		if difference < 0 {
			difference = -difference
		}
		if difference < bestDifference {
			bestDifference = difference
			bestPosition = position
			bestLeftTotal = leftTotal
		}
	}

	rightRecords := slices.Clone(page.records[bestPosition:])
	rightOccupied := total - bestLeftTotal
	page.records = slices.Clone(page.records[:bestPosition])
	page.occupied = total - rightOccupied

	rightPage := &leafPage{
		id:       t.nextPage,
		records:  rightRecords,
		occupied: rightOccupied,
	}
	t.nextPage++
	t.pages = slices.Insert(t.pages, pageIndex+1, rightPage)
}

func leftSum(records []record) int {
	total := 0
	for _, item := range records {
		total += item.size
	}
	return total
}

func (t *ByteBPlusTree) minimumOccupancy() int {
	return (t.capacity + 1) / 2
}

func (t *ByteBPlusTree) rebalance(underflowIndex int) {
	if t.borrowFromLeft(underflowIndex) {
		return
	}
	if t.borrowFromRight(underflowIndex) {
		return
	}
	if t.mergeWithLeft(underflowIndex) {
		return
	}
	_ = t.mergeWithRight(underflowIndex)
}

func (t *ByteBPlusTree) borrowFromLeft(index int) bool {
	if index == 0 {
		return false
	}

	minimum := t.minimumOccupancy()
	left := t.pages[index-1]
	underflow := t.pages[index]
	movedSize := 0
	movedCount := 0

	for movedCount < len(left.records) {
		movedSize += left.records[len(left.records)-1-movedCount].size
		movedCount++
		if underflow.occupied+movedSize >= minimum && left.occupied-movedSize >= minimum {
			break
		}
	}

	if underflow.occupied+movedSize < minimum || left.occupied-movedSize < minimum {
		return false
	}

	start := len(left.records) - movedCount
	movedRecords := slices.Clone(left.records[start:])
	underflow.records = append(slices.Clone(movedRecords), underflow.records...)
	underflow.occupied += movedSize
	left.records = slices.Delete(left.records, start, len(left.records))
	left.occupied -= movedSize
	return true
}

func (t *ByteBPlusTree) borrowFromRight(index int) bool {
	if index == len(t.pages)-1 {
		return false
	}

	minimum := t.minimumOccupancy()
	underflow := t.pages[index]
	right := t.pages[index+1]
	movedSize := 0
	movedCount := 0

	for movedCount < len(right.records) {
		movedSize += right.records[movedCount].size
		movedCount++
		if underflow.occupied+movedSize >= minimum && right.occupied-movedSize >= minimum {
			break
		}
	}

	if underflow.occupied+movedSize < minimum || right.occupied-movedSize < minimum {
		return false
	}

	underflow.records = append(underflow.records, slices.Clone(right.records[:movedCount])...)
	underflow.occupied += movedSize
	right.records = slices.Delete(right.records, 0, movedCount)
	right.occupied -= movedSize
	return true
}

func (t *ByteBPlusTree) mergeWithLeft(index int) bool {
	if index == 0 {
		return false
	}

	left := t.pages[index-1]
	underflow := t.pages[index]
	if left.occupied+underflow.occupied > t.capacity {
		return false
	}

	left.records = append(left.records, underflow.records...)
	left.occupied += underflow.occupied
	t.pages = slices.Delete(t.pages, index, index+1)
	return true
}

func (t *ByteBPlusTree) mergeWithRight(index int) bool {
	if index == len(t.pages)-1 {
		return false
	}

	underflow := t.pages[index]
	right := t.pages[index+1]
	if underflow.occupied+right.occupied > t.capacity {
		return false
	}

	underflow.records = append(underflow.records, right.records...)
	underflow.occupied += right.occupied
	t.pages = slices.Delete(t.pages, index+1, index+2)
	return true
}
