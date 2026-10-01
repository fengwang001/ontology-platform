package ontology

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidPerKeyLimit      = errors.New("per-key entry limit must be at least 1")
	ErrInvalidGlobalLimit      = errors.New("global entry limit must be at least 1")
	ErrEmptyKey                = errors.New("action key must not be empty")
	ErrEmptyManifest           = errors.New("read manifest must not be empty")
	ErrEmptyPath               = errors.New("read manifest path must not be empty")
	ErrPathsNotStrictlyOrdered = errors.New("read manifest paths must be strictly increasing")
	ErrEmptyDigest             = errors.New("content digest must not be empty")
	ErrEmptyResult             = errors.New("result digest must not be empty")
)

type ReadItem struct {
	Path   string
	Digest string
}

type Entry struct {
	Manifest []ReadItem
	Result   string
	Last     uint64
}

type Cache struct {
	mu       sync.Mutex
	tick     uint64
	entries  map[string][]*entry
	perKey   int
	global   int
	examined uint64
}

type entry struct {
	manifest []ReadItem
	result   string
	last     uint64
}

func New(perKeyLimit, globalLimit int) (*Cache, error) {
	if perKeyLimit < 1 {
		return nil, ErrInvalidPerKeyLimit
	}
	if globalLimit < 1 {
		return nil, ErrInvalidGlobalLimit
	}

	return &Cache{
		entries: make(map[string][]*entry),
		perKey:  perKeyLimit,
		global:  globalLimit,
	}, nil
}

func (c *Cache) Put(key string, manifest []ReadItem, result string) error {
	if key == "" {
		return ErrEmptyKey
	}
	if err := validateManifest(manifest); err != nil {
		return err
	}
	if result == "" {
		return ErrEmptyResult
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	bucket := c.entries[key]
	manifestCopy := cloneManifest(manifest)

	for _, existing := range bucket {
		if manifestsEqual(existing.manifest, manifestCopy) {
			existing.result = result
			c.tick++
			existing.last = c.tick
			return nil
		}
	}

	added := &entry{
		manifest: manifestCopy,
		result:   result,
	}
	c.tick++
	added.last = c.tick
	bucket = append(bucket, added)

	for len(bucket) > c.perKey {
		oldest := indexOfOldest(bucket)
		bucket = removeEntry(bucket, oldest)
	}
	c.entries[key] = bucket

	for c.lenLocked() > c.global {
		oldestKey := ""
		oldestIndex := -1
		for bucketKey, entries := range c.entries {
			index := indexOfOldest(entries)
			if oldestIndex == -1 || entries[index].last < c.entries[oldestKey][oldestIndex].last {
				oldestKey = bucketKey
				oldestIndex = index
			}
		}

		oldestBucket := c.entries[oldestKey]
		oldestBucket = removeEntry(oldestBucket, oldestIndex)
		if len(oldestBucket) == 0 {
			delete(c.entries, oldestKey)
		} else {
			c.entries[oldestKey] = oldestBucket
		}
	}

	return nil
}

func (c *Cache) Lookup(key string, current map[string]string) (string, bool, error) {
	if key == "" {
		return "", false, ErrEmptyKey
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.examined = 0
	bucket := c.entries[key]
	var match *entry
	for _, candidate := range bucket {
		c.examined++
		if manifestMatches(candidate.manifest, current) && (match == nil || candidate.last > match.last) {
			match = candidate
		}
	}

	if match == nil {
		return "", false, nil
	}

	c.tick++
	match.last = c.tick
	return match.result, true, nil
}

func (c *Cache) Dump(key string) []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()

	if key == "" {
		return []Entry{}
	}

	bucket := c.entries[key]
	if len(bucket) == 0 {
		return []Entry{}
	}

	entries := make([]Entry, len(bucket))
	for i, current := range bucket {
		entries[i] = Entry{
			Manifest: current.manifest,
			Result:   current.result,
			Last:     current.last,
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Last > entries[j].Last
	})

	for i := range entries {
		entries[i].Manifest = cloneManifest(entries[i].Manifest)
	}

	return entries
}

func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.lenLocked()
}

func validateManifest(manifest []ReadItem) error {
	if len(manifest) == 0 {
		return ErrEmptyManifest
	}

	for i := range manifest {
		if manifest[i].Path == "" {
			return ErrEmptyPath
		}
	}

	for i := 1; i < len(manifest); i++ {
		if manifest[i].Path <= manifest[i-1].Path {
			return ErrPathsNotStrictlyOrdered
		}
	}

	for i := range manifest {
		if manifest[i].Digest == "" {
			return ErrEmptyDigest
		}
	}

	return nil
}

func cloneManifest(manifest []ReadItem) []ReadItem {
	copyManifest := make([]ReadItem, len(manifest))
	copy(copyManifest, manifest)
	return copyManifest
}

func manifestsEqual(left, right []ReadItem) bool {
	if len(left) != len(right) {
		return false
	}

	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}

	return true
}

func manifestMatches(manifest []ReadItem, current map[string]string) bool {
	for _, item := range manifest {
		digest, ok := current[item.Path]
		if !ok || digest != item.Digest {
			return false
		}
	}

	return true
}

func indexOfOldest(entries []*entry) int {
	oldest := 0
	for i := 1; i < len(entries); i++ {
		if entries[i].last < entries[oldest].last {
			oldest = i
		}
	}
	return oldest
}

func removeEntry(entries []*entry, index int) []*entry {
	return append(entries[:index], entries[index+1:]...)
}

func (c *Cache) lenLocked() int {
	total := 0
	for _, entries := range c.entries {
		total += len(entries)
	}
	return total
}
