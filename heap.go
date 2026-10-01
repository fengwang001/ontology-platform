package heap

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidSize          = errors.New("heap: page count or slot count must be positive")
	ErrInvalidTransactionID = errors.New("heap: transaction id must be positive")
	ErrNoEmptySlot          = errors.New("heap: all pages are full")
	ErrInvalidPage          = errors.New("heap: page is out of range")
	ErrInvalidSlot          = errors.New("heap: slot is out of range")
	ErrEmptySlot            = errors.New("heap: slot is empty")
	ErrAlreadyDeleted       = errors.New("heap: row is already deleted")
	ErrInvalidSnapshotValue = errors.New("heap: snapshot value must be positive")
	ErrSnapshotTooOld       = errors.New("heap: snapshot value is below the global vacuum horizon")
	ErrUnknownSnapshot      = errors.New("heap: snapshot id is unknown")
	ErrInvalidVacuumHorizon = errors.New("heap: vacuum horizon must be positive")
	ErrActiveSnapshotTooOld = errors.New("heap: an active snapshot is below the vacuum horizon")
	ErrInvalidKeyRange      = errors.New("heap: scan lower bound must not exceed upper bound")
)

type Location struct {
	Page int
	Slot int
}

type Row struct {
	Key  string
	Page int
	Slot int
}

type ScanResult struct {
	Rows           []Row
	HeapFetches    int
	IndexOnlyReads int
}

func NewHeap(pageCount, slotCount int) (*Heap, error) {
	if pageCount < 1 || slotCount < 1 {
		return nil, ErrInvalidSize
	}

	pages := make([][]record, pageCount)
	for page := range pages {
		pages[page] = make([]record, slotCount)
	}

	return &Heap{
		pages:      pages,
		allVisible: make([]bool, pageCount),
		snapshots:  make(map[int]int),
	}, nil
}

func (h *Heap) Insert(key string, xid int) (Location, error) {
	if xid <= 0 {
		return Location{}, ErrInvalidTransactionID
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	for page := range h.pages {
		for slot, row := range h.pages[page] {
			if !row.used {
				location := Location{Page: page, Slot: slot}
				h.pages[page][slot] = record{
					used: true,
					key:  key,
					xmin: xid,
				}
				h.insertIndex(indexEntry{
					key:      key,
					page:     page,
					slot:     slot,
					location: location,
				})
				h.allVisible[page] = false
				return location, nil
			}
		}
	}

	return Location{}, ErrNoEmptySlot
}

func (h *Heap) Delete(page, slot, xid int) error {
	if xid <= 0 {
		return ErrInvalidTransactionID
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if page < 0 || page >= len(h.pages) {
		return ErrInvalidPage
	}
	if slot < 0 || slot >= len(h.pages[page]) {
		return ErrInvalidSlot
	}
	if !h.pages[page][slot].used {
		return ErrEmptySlot
	}
	if h.pages[page][slot].xmax != 0 {
		return ErrAlreadyDeleted
	}

	h.pages[page][slot].xmax = xid
	h.allVisible[page] = false
	return nil
}

func (h *Heap) Snapshot(s int) (int, error) {
	if s <= 0 {
		return 0, ErrInvalidSnapshotValue
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if s < h.globalHorizon {
		return 0, ErrSnapshotTooOld
	}

	h.nextSnapshotID++
	id := h.nextSnapshotID
	h.snapshots[id] = s
	return id, nil
}

func (h *Heap) Release(id int) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.snapshots[id]; !ok {
		return ErrUnknownSnapshot
	}
	delete(h.snapshots, id)
	return nil
}

func (h *Heap) Vacuum(page, horizon int) error {
	if horizon <= 0 {
		return ErrInvalidVacuumHorizon
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if page < 0 || page >= len(h.pages) {
		return ErrInvalidPage
	}
	for _, snapshot := range h.snapshots {
		if snapshot < horizon {
			return ErrActiveSnapshotTooOld
		}
	}

	for slot, row := range h.pages[page] {
		if row.used && row.xmax != 0 && row.xmax < horizon {
			h.removeIndex(row.key, page, slot)
			h.pages[page][slot] = record{}
		}
	}

	visible := true
	for _, row := range h.pages[page] {
		if row.used && (row.xmin >= horizon || row.xmax != 0) {
			visible = false
			break
		}
	}
	h.allVisible[page] = visible
	if horizon > h.globalHorizon {
		h.globalHorizon = horizon
	}
	return nil
}

func (h *Heap) Scan(lo, hi string, snapshotID int) (ScanResult, error) {
	if lo > hi {
		return ScanResult{}, ErrInvalidKeyRange
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	snapshot, ok := h.snapshots[snapshotID]
	if !ok {
		return ScanResult{}, ErrUnknownSnapshot
	}

	result := ScanResult{Rows: []Row{}}
	for _, entry := range h.index {
		if entry.key < lo {
			continue
		}
		if entry.key >= hi {
			break
		}

		row := h.pages[entry.page][entry.slot]
		if h.allVisible[entry.page] {
			result.IndexOnlyReads++
		} else {
			result.HeapFetches++
			if !visible(row, snapshot) {
				continue
			}
		}

		result.Rows = append(result.Rows, Row{
			Key:  row.key,
			Page: entry.page,
			Slot: entry.slot,
		})
	}

	return result, nil
}

type record struct {
	used bool
	key  string
	xmin int
	xmax int
}

type indexEntry struct {
	key      string
	page     int
	slot     int
	location Location
}

type Heap struct {
	mu             sync.Mutex
	pages          [][]record
	index          []indexEntry
	allVisible     []bool
	snapshots      map[int]int
	nextSnapshotID int
	globalHorizon  int
}

func (h *Heap) insertIndex(entry indexEntry) {
	position := sort.Search(len(h.index), func(i int) bool {
		return compareIndexEntry(h.index[i], entry) >= 0
	})
	h.index = append(h.index, indexEntry{})
	copy(h.index[position+1:], h.index[position:])
	h.index[position] = entry
}

func (h *Heap) removeIndex(key string, page, slot int) {
	target := indexEntry{key: key, page: page, slot: slot}
	position := sort.Search(len(h.index), func(i int) bool {
		return compareIndexEntry(h.index[i], target) >= 0
	})
	if position < len(h.index) && compareIndexEntry(h.index[position], target) == 0 {
		h.index = append(h.index[:position], h.index[position+1:]...)
	}
}

func compareIndexEntry(left, right indexEntry) int {
	switch {
	case left.key < right.key:
		return -1
	case left.key > right.key:
		return 1
	case left.page < right.page:
		return -1
	case left.page > right.page:
		return 1
	case left.slot < right.slot:
		return -1
	case left.slot > right.slot:
		return 1
	default:
		return 0
	}
}

func visible(row record, snapshot int) bool {
	return row.xmin < snapshot && (row.xmax == 0 || row.xmax >= snapshot)
}
