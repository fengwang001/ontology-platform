package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrExists          = errors.New("already exists")
	ErrHashFull        = errors.New("same-hash slot is full")
	ErrCursorExpired   = errors.New("cursor is expired")
)

type Key uint64

type Cookie struct {
	Gen uint64
	Pos Key
}

type DirEntry struct {
	Key  Key
	Name string
	Ino  uint64
}

type HashDirectory struct {
	mu      sync.RWMutex
	seed    uint32
	limit   int
	gen     uint64
	byName  map[string]*entry
	entries map[uint64]map[uint32]*entry
	prevGen uint64
	prev    map[Key]string
}

type entry struct {
	name  string
	ino   uint64
	h     uint32
	minor uint32
}

func hashName(seed uint32, name string) uint32 {
	h := uint32(2166136261) ^ seed
	for i := 0; i < len(name); i++ {
		h = (h ^ uint32(name[i])) * 16777619
	}
	return h
}

func entryKey(h uint32, minor uint32) Key {
	return Key(uint64(h)<<32 | uint64(minor))
}

func (e *entry) key() Key {
	return entryKey(e.h, e.minor)
}

func NewHashDirectory(seed uint32, limit int) (*HashDirectory, error) {
	if limit < 1 || limit > 1<<31-1 {
		return nil, ErrInvalidArgument
	}
	return &HashDirectory{
		seed:    seed,
		limit:   limit,
		gen:     1,
		byName:  make(map[string]*entry),
		entries: make(map[uint64]map[uint32]*entry),
	}, nil
}

func (d *HashDirectory) Add(name string, ino uint64) (Key, error) {
	if name == "" {
		return 0, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, ok := d.byName[name]; ok {
		return 0, ErrExists
	}
	h := hashName(d.seed, name)
	bucket := d.entries[uint64(h)]
	if len(bucket) >= d.limit {
		return 0, ErrHashFull
	}
	minor := firstFreeMinor(bucket)
	item := &entry{name: name, ino: ino, h: h, minor: minor}
	d.insert(item)
	return item.key(), nil
}

func (d *HashDirectory) Remove(name string) error {
	if name == "" {
		return ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	item, ok := d.byName[name]
	if !ok {
		return ErrNotFound
	}
	d.erase(item)
	return nil
}

func (d *HashDirectory) Rename(oldName, newName string) (Key, error) {
	if oldName == "" || newName == "" {
		return 0, ErrInvalidArgument
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	old, ok := d.byName[oldName]
	if !ok {
		return 0, ErrNotFound
	}
	if newName == oldName {
		return 0, ErrExists
	}
	if _, ok := d.byName[newName]; ok {
		return 0, ErrExists
	}

	newHash := hashName(d.seed, newName)
	oldHash := old.h
	available := d.limit - len(d.entries[uint64(newHash)])
	if oldHash == newHash {
		available++
	}
	if available <= 0 {
		return 0, ErrHashFull
	}

	ino := old.ino
	d.erase(old)
	bucket := d.entries[uint64(newHash)]
	item := &entry{name: newName, ino: ino, h: newHash, minor: firstFreeMinor(bucket)}
	d.insert(item)
	return item.key(), nil
}

func (d *HashDirectory) ReadDir(c Cookie, n int) ([]DirEntry, Cookie, bool, error) {
	if n < 0 {
		return nil, Cookie{}, false, ErrInvalidArgument
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	cursor, err := d.normalizeCookie(c)
	if err != nil {
		return nil, Cookie{}, false, err
	}
	if n == 0 {
		return []DirEntry{}, cursor, !d.hasAtLeast(cursor.Pos), nil
	}

	items := make([]*entry, 0)
	for _, bucket := range d.entries {
		for minor, item := range bucket {
			if entryKey(item.h, minor) >= cursor.Pos {
				items = append(items, item)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].key() < items[j].key()
	})
	if len(items) > n {
		items = items[:n]
	}

	result := make([]DirEntry, 0, len(items))
	for _, item := range items {
		result = append(result, DirEntry{Key: item.key(), Name: item.name, Ino: item.ino})
	}
	if len(items) == 0 {
		return result, cursor, !d.hasAtLeast(cursor.Pos), nil
	}
	next := Cookie{Gen: d.gen, Pos: items[len(items)-1].key() + 1}
	return result, next, !d.hasAtLeast(next.Pos), nil
}

func (d *HashDirectory) CookieOf(name string) (Cookie, error) {
	if name == "" {
		return Cookie{}, ErrInvalidArgument
	}
	d.mu.RLock()
	defer d.mu.RUnlock()

	item, ok := d.byName[name]
	if !ok {
		return Cookie{}, ErrNotFound
	}
	return Cookie{Gen: d.gen, Pos: item.key() + 1}, nil
}

func (d *HashDirectory) Rehash(newSeed uint32) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	names := make([]string, 0, len(d.byName))
	for name := range d.byName {
		names = append(names, name)
	}
	sort.Strings(names)

	type pending struct {
		item   *entry
		bucket map[uint32]*entry
	}
	newEntries := make(map[uint64]map[uint32]*entry)
	pendingItems := make([]pending, 0, len(names))
	for _, name := range names {
		h := hashName(newSeed, name)
		bucket := newEntries[uint64(h)]
		if len(bucket) >= d.limit {
			return ErrHashFull
		}
		if bucket == nil {
			bucket = make(map[uint32]*entry)
			newEntries[uint64(h)] = bucket
		}
		old := d.byName[name]
		item := &entry{name: name, ino: old.ino, h: h, minor: firstFreeMinor(bucket)}
		bucket[item.minor] = item
		pendingItems = append(pendingItems, pending{item: item, bucket: bucket})
	}

	snapshot := make(map[Key]string, len(d.byName))
	for name, item := range d.byName {
		snapshot[item.key()] = name
	}

	d.prevGen = d.gen
	d.prev = snapshot
	d.seed = newSeed
	d.gen++
	d.entries = newEntries
	newByName := make(map[string]*entry, len(d.byName))
	for _, item := range pendingItems {
		newByName[item.item.name] = item.item
	}
	d.byName = newByName
	return nil
}

func (d *HashDirectory) insert(item *entry) {
	bucket := d.entries[uint64(item.h)]
	if bucket == nil {
		bucket = make(map[uint32]*entry)
		d.entries[uint64(item.h)] = bucket
	}
	bucket[item.minor] = item
	d.byName[item.name] = item
}

func (d *HashDirectory) erase(item *entry) {
	bucket := d.entries[uint64(item.h)]
	delete(bucket, item.minor)
	if len(bucket) == 0 {
		delete(d.entries, uint64(item.h))
	}
	delete(d.byName, item.name)
}

func firstFreeMinor(bucket map[uint32]*entry) uint32 {
	for minor := uint32(0); ; minor++ {
		if _, ok := bucket[minor]; !ok {
			return minor
		}
	}
}

func (d *HashDirectory) normalizeCookie(c Cookie) (Cookie, error) {
	if c.Pos == 0 {
		return Cookie{Gen: d.gen, Pos: 0}, nil
	}
	if c.Gen == d.gen {
		return c, nil
	}
	if c.Gen+1 == d.gen && d.prevGen == c.Gen {
		name, ok := d.prev[c.Pos-1]
		if ok {
			if item, exists := d.byName[name]; exists {
				return Cookie{Gen: d.gen, Pos: item.key() + 1}, nil
			}
		}
	}
	return Cookie{}, ErrCursorExpired
}

func (d *HashDirectory) hasAtLeast(pos Key) bool {
	for _, bucket := range d.entries {
		for _, item := range bucket {
			if item.key() >= pos {
				return true
			}
		}
	}
	return false
}
