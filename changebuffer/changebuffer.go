package changebuffer

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrPageNotInPool   = errors.New("page not in pool")
	ErrPageOutOfSpace  = errors.New("page out of space")
)

type Kind string

const (
	Insert     Kind = "Insert"
	DeleteMark Kind = "DeleteMark"
	Purge      Kind = "Purge"
)

type Operation struct {
	Page int
	Kind Kind
	Key  []byte
	Size int64
}

type Entry struct {
	Key     []byte
	Size    int64
	Deleted bool
}

type ChangeBuffer struct {
	mu          sync.RWMutex
	pageSize    int64
	maxPerPage  int
	globalLimit int64
	pages       map[int]*page
	globalBuf   int64
}

type pageEntry struct {
	size    int64
	deleted bool
}

type queuedOperation struct {
	kind Kind
	key  string
	size int64
}

type page struct {
	inPool   bool
	entries  map[string]pageEntry
	used     int64
	queue    []queuedOperation
	bufBytes int64
}

func New(pageSize int64, maxPerPage int, globalLimit int64) (*ChangeBuffer, error) {
	if pageSize < 64 || pageSize > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	if maxPerPage < 1 || maxPerPage > 1_000_000 {
		return nil, ErrInvalidArgument
	}
	if globalLimit < 1 || globalLimit > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &ChangeBuffer{
		pageSize:    pageSize,
		maxPerPage:  maxPerPage,
		globalLimit: globalLimit,
		pages:       make(map[int]*page),
	}, nil
}

func (cb *ChangeBuffer) Op(page int, kind Kind, key []byte, size int64) error {
	if err := validatePage(page); err != nil {
		return err
	}
	if len(key) == 0 {
		return ErrInvalidArgument
	}
	if !validKind(kind) {
		return ErrInvalidArgument
	}
	if !validSize(kind, size, cb.pageSize) {
		return ErrInvalidArgument
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	target := cb.page(page)
	if target.inPool {
		if !applyEntry(target.entries, &target.used, cb.pageSize, kind, string(key), size) {
			return ErrPageOutOfSpace
		}
		return nil
	}

	op := queuedOperation{kind: kind, key: string(key), size: size}
	if cb.canBuffer(target, op) {
		target.queue = append(target.queue, op)
		if kind == Insert {
			target.bufBytes += size
			cb.globalBuf += size
		}
		return nil
	}

	merged := clonePage(target)
	for _, queued := range merged.queue {
		applyEntry(merged.entries, &merged.used, cb.pageSize, queued.kind, queued.key, queued.size)
	}
	merged.queue = nil
	merged.bufBytes = 0
	if !applyEntry(merged.entries, &merged.used, cb.pageSize, kind, string(key), size) {
		return ErrPageOutOfSpace
	}

	merged.inPool = true
	cb.globalBuf -= target.bufBytes
	cb.pages[page] = merged
	return nil
}

func (cb *ChangeBuffer) Load(page int) error {
	if err := validatePage(page); err != nil {
		return err
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	target := cb.page(page)
	if target.inPool {
		return nil
	}

	for _, queued := range target.queue {
		applyEntry(target.entries, &target.used, cb.pageSize, queued.kind, queued.key, queued.size)
	}
	cb.globalBuf -= target.bufBytes
	target.queue = nil
	target.bufBytes = 0
	target.inPool = true
	return nil
}

func (cb *ChangeBuffer) Evict(page int) error {
	if err := validatePage(page); err != nil {
		return err
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	target := cb.page(page)
	if !target.inPool {
		return ErrPageNotInPool
	}
	target.inPool = false
	return nil
}

func (cb *ChangeBuffer) View(page int) ([]Entry, error) {
	if err := validatePage(page); err != nil {
		return nil, err
	}

	cb.mu.RLock()
	defer cb.mu.RUnlock()

	target := cb.pages[page]
	logical := clonePage(target)
	for _, queued := range logical.queue {
		applyEntry(logical.entries, &logical.used, cb.pageSize, queued.kind, queued.key, queued.size)
	}

	keys := make([]string, 0, len(logical.entries))
	for key := range logical.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]Entry, 0, len(keys))
	for _, key := range keys {
		item := logical.entries[key]
		result = append(result, Entry{
			Key:     []byte(key),
			Size:    item.size,
			Deleted: item.deleted,
		})
	}
	return result, nil
}

func validatePage(page int) error {
	if page < 0 || page > 1_000_000 {
		return ErrInvalidArgument
	}
	return nil
}

func validKind(kind Kind) bool {
	return kind == Insert || kind == DeleteMark || kind == Purge
}

func validSize(kind Kind, size int64, pageSize int64) bool {
	if kind == Insert {
		return size >= 1 && size <= pageSize
	}
	return size == 0
}

func (cb *ChangeBuffer) page(number int) *page {
	target := cb.pages[number]
	if target == nil {
		target = &page{entries: make(map[string]pageEntry)}
		cb.pages[number] = target
	}
	return target
}

func (cb *ChangeBuffer) canBuffer(target *page, op queuedOperation) bool {
	if len(target.queue) >= cb.maxPerPage {
		return false
	}
	if op.kind != Insert {
		return true
	}
	free := cb.pageSize - target.used
	return target.bufBytes+op.size <= bucketLowerBound(cb.pageSize, bucket(cb.pageSize, free)) &&
		cb.globalBuf+op.size <= cb.globalLimit
}

func bucket(pageSize, free int64) int {
	if 32*free < pageSize {
		return 0
	}
	if 16*free < pageSize {
		return 1
	}
	if 8*free < pageSize {
		return 2
	}
	return 3
}

func bucketLowerBound(pageSize int64, bucketNumber int) int64 {
	switch bucketNumber {
	case 1:
		return pageSize / 32
	case 2:
		return pageSize / 16
	case 3:
		return pageSize / 8
	default:
		return 0
	}
}

func applyEntry(entries map[string]pageEntry, used *int64, pageSize int64, kind Kind, key string, size int64) bool {
	switch kind {
	case Insert:
		if item, ok := entries[key]; ok {
			item.deleted = false
			entries[key] = item
			return true
		}
		if pageSize-*used < size {
			return false
		}
		entries[key] = pageEntry{size: size}
		*used += size
		return true
	case DeleteMark:
		if item, ok := entries[key]; ok {
			item.deleted = true
			entries[key] = item
		}
		return true
	case Purge:
		if item, ok := entries[key]; ok && item.deleted {
			delete(entries, key)
			*used -= item.size
		}
		return true
	default:
		return false
	}
}

func clonePage(source *page) *page {
	if source == nil {
		return &page{entries: make(map[string]pageEntry)}
	}
	clone := &page{
		inPool:   source.inPool,
		entries:  make(map[string]pageEntry, len(source.entries)),
		used:     source.used,
		bufBytes: source.bufBytes,
	}
	for key, item := range source.entries {
		clone.entries[key] = item
	}
	clone.queue = append(clone.queue, source.queue...)
	return clone
}
