// Package retention implements a tiered history-version retention cleaner.
//
// A Cleaner keeps, per file, a set of historical versions each stamped with
// a time and a size. Versions are thinned according to the age tier they
// fall into: each tier defines a minimum spacing between kept versions, so
// older history is kept at coarser granularity. Cleaning is further bounded
// by pinned versions, a maximum version count and a maximum byte total per
// file. Deleted versions move to a trash where they can be restored until
// a TTL expires, after which Clean purges them for good.
package retention

import (
	"errors"
	"sort"
	"sync"
)

// maxFileBytes is the hard cap (10^15) on the summed size of the active
// versions of a single file. Add reports ErrTooLarge beyond it.
const maxFileBytes int64 = 1_000_000_000_000_000

// Rule describes one age tier. A version of age a belongs to the tier with
// the smallest index whose Until satisfies a < Until (a == Until falls into
// the next tier). Step is the minimum spacing kept between consecutive
// retained versions inside the tier. The last tier's Until is also the
// maximum age: versions at or beyond it are over-aged.
type Rule struct {
	Until int64
	Step  int64
}

// Reason explains why Clean deleted a version.
type Reason int

const (
	// ReasonAged: unpinned, non-latest version past the maximum age.
	ReasonAged Reason = iota
	// ReasonThinned: spacing to the previous kept version below the tier Step.
	ReasonThinned
	// ReasonOverCount: deleted while the version count exceeded maxCount.
	ReasonOverCount
	// ReasonOverBytes: deleted while the byte total exceeded maxBytes
	// (and the count did not exceed maxCount).
	ReasonOverBytes
)

func (r Reason) String() string {
	switch r {
	case ReasonAged:
		return "Aged"
	case ReasonThinned:
		return "Thinned"
	case ReasonOverCount:
		return "OverCount"
	case ReasonOverBytes:
		return "OverBytes"
	}
	return "Unknown"
}

var (
	// ErrClock reports a now smaller than any previously accepted now.
	ErrClock = errors.New("retention: clock moved backwards")
	// ErrInvalid reports a violated argument constraint.
	ErrInvalid = errors.New("retention: invalid argument")
	// ErrDuplicate reports an Add for an already known (file, t).
	ErrDuplicate = errors.New("retention: duplicate version")
	// ErrNoVersion reports a reference to a version that does not exist
	// in the active set (or, for Undelete, recoverably in the trash).
	ErrNoVersion = errors.New("retention: no such version")
	// ErrTooLarge reports an Add that would push a file's active byte
	// total past 10^15.
	ErrTooLarge = errors.New("retention: file byte total exceeds 10^15")
)

// Version is one active historical version of a file.
type Version struct {
	T      int64
	Size   int64
	Pinned bool
}

// TrashEntry is one deleted version still held in the trash.
type TrashEntry struct {
	T         int64
	Size      int64
	DeletedAt int64
}

// Deletion records a version Clean moved to the trash, with its reason.
type Deletion struct {
	File   string
	T      int64
	Reason Reason
}

// Purge records a trash entry Clean removed permanently.
type Purge struct {
	File string
	T    int64
}

// CleanResult is the outcome of one Clean call.
type CleanResult struct {
	// Deleted lists versions moved to the trash, ordered by file (byte
	// order) then by t ascending.
	Deleted []Deletion
	// Purged lists trash entries permanently removed, ordered by
	// (file, t) ascending.
	Purged []Purge
}

// Cleaner is a concurrency-safe tiered retention cleaner. All methods may
// be called concurrently; the result is equivalent to some serial order.
type Cleaner struct {
	mu       sync.Mutex
	rules    []Rule
	maxCount int64
	maxBytes int64
	trashTTL int64

	hasNow  bool
	lastNow int64

	files map[string]*fileState

	// thinnedScans counts versions examined by the Thinned phase of Clean
	// (unexported; asserted by tests to equal the number of versions left
	// after the Aged phase, summed over files).
	thinnedScans int64
}

type version struct {
	t         int64
	size      int64
	pinned    bool
	deleted   bool
	deletedAt int64
}

type fileState struct {
	versions map[int64]*version
}

func newFileState() *fileState {
	return &fileState{versions: make(map[int64]*version)}
}

// active returns the live versions ordered by t ascending.
func (f *fileState) active() []*version {
	var out []*version
	for _, v := range f.versions {
		if !v.deleted {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t < out[j].t })
	return out
}

// trashed returns the trash entries ordered by t ascending.
func (f *fileState) trashed() []*version {
	var out []*version
	for _, v := range f.versions {
		if v.deleted {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t < out[j].t })
	return out
}

// activeBytes sums the sizes of the live versions.
func (f *fileState) activeBytes() int64 {
	var sum int64
	for _, v := range f.versions {
		if !v.deleted {
			sum += v.size
		}
	}
	return sum
}

// NewCleaner validates the configuration and returns a ready Cleaner.
func NewCleaner(rules []Rule, maxCount, maxBytes, trashTTL int64) (*Cleaner, error) {
	if len(rules) == 0 {
		return nil, ErrInvalid
	}
	prevUntil := int64(0)
	for _, r := range rules {
		if r.Until < 1 || r.Step < 1 || r.Until <= prevUntil {
			return nil, ErrInvalid
		}
		prevUntil = r.Until
	}
	if maxCount < 1 || maxBytes < 1 || trashTTL < 1 {
		return nil, ErrInvalid
	}
	cp := make([]Rule, len(rules))
	copy(cp, rules)
	return &Cleaner{
		rules:    cp,
		maxCount: maxCount,
		maxBytes: maxBytes,
		trashTTL: trashTTL,
		files:    make(map[string]*fileState),
	}, nil
}

// Add records a new version of file at time t with the given size.
//
// Error precedence: ErrClock > ErrInvalid > ErrDuplicate > ErrTooLarge.
func (c *Cleaner) Add(now int64, file string, t, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if now < 0 || file == "" || t < 0 || t > now || size < 1 {
		return ErrInvalid
	}
	fs := c.files[file]
	if fs != nil {
		if _, ok := fs.versions[t]; ok {
			// Known t is a duplicate whether the version is live or
			// still sitting in the trash (even past its TTL).
			return ErrDuplicate
		}
	}
	var sum int64
	if fs != nil {
		sum = fs.activeBytes()
	}
	if size > maxFileBytes-sum {
		return ErrTooLarge
	}
	if fs == nil {
		fs = newFileState()
		c.files[file] = fs
	}
	fs.versions[t] = &version{t: t, size: size}
	c.acceptNow(now)
	return nil
}

// Pin marks a version as pinned. Pinning an already pinned version succeeds.
func (c *Cleaner) Pin(file string, t int64) error {
	return c.setPinned(file, t, true)
}

// Unpin clears the pinned mark. Unpinning an unpinned version succeeds.
func (c *Cleaner) Unpin(file string, t int64) error {
	return c.setPinned(file, t, false)
}

func (c *Cleaner) setPinned(file string, t int64, pinned bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.files[file]
	if fs == nil {
		return ErrNoVersion
	}
	v, ok := fs.versions[t]
	if !ok || v.deleted {
		return ErrNoVersion
	}
	v.pinned = pinned
	return nil
}

// Clean purges expired trash entries, then applies the Aged, Thinned and
// limit phases to every file.
func (c *Cleaner) Clean(now int64) (CleanResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var res CleanResult
	if err := c.checkClock(now); err != nil {
		return res, err
	}
	if now < 0 {
		return res, ErrInvalid
	}

	// Phase 0: permanently purge trash entries whose TTL has elapsed.
	// Iterating files in byte order and entries by t keeps Purged sorted.
	for _, name := range c.sortedFiles() {
		fs := c.files[name]
		for _, v := range fs.trashed() {
			if now-v.deletedAt >= c.trashTTL {
				delete(fs.versions, v.t)
				res.Purged = append(res.Purged, Purge{File: name, T: v.t})
			}
		}
	}

	// Phases 1-3 per file, files in byte order.
	for _, name := range c.sortedFiles() {
		res.Deleted = append(res.Deleted, c.cleanFile(now, name, c.files[name])...)
	}

	c.acceptNow(now)
	return res, nil
}

// cleanFile runs the Aged, Thinned and limit phases for one file and moves
// the deleted versions to the trash. The returned deletions are ordered by
// t ascending.
func (c *Cleaner) cleanFile(now int64, name string, fs *fileState) []Deletion {
	active := fs.active()
	if len(active) == 0 {
		return nil
	}
	latest := active[len(active)-1].t
	maxAge := c.rules[len(c.rules)-1].Until

	var deleted []Deletion
	remove := func(v *version, reason Reason) {
		v.deleted = true
		v.deletedAt = now
		deleted = append(deleted, Deletion{File: name, T: v.t, Reason: reason})
	}

	// Phase 1 (Aged): unpinned, non-latest versions at or beyond the
	// maximum age.
	remaining := active[:0]
	for _, v := range active {
		if !v.pinned && v.t != latest && now-v.t >= maxAge {
			remove(v, ReasonAged)
		} else {
			remaining = append(remaining, v)
		}
	}

	// Phase 2 (Thinned): scan by t ascending, keeping a minimum spacing
	// of the candidate's tier Step against the previously kept version.
	kept := remaining[:0]
	var prev *version
	for _, v := range remaining {
		c.thinnedScans++
		if v.pinned || v.t == latest || prev == nil {
			kept = append(kept, v)
			prev = v
			continue
		}
		if v.t-prev.t < c.stepFor(now-v.t) {
			remove(v, ReasonThinned)
		} else {
			kept = append(kept, v)
			prev = v
		}
	}

	// Phase 3 (limits): while over maxCount or maxBytes, drop the oldest
	// unpinned, non-latest version. Pinned and latest versions are never
	// deleted, even if the limits stay exceeded.
	count := int64(len(kept))
	var bytes int64
	for _, v := range kept {
		bytes += v.size
	}
	for count > c.maxCount || bytes > c.maxBytes {
		idx := -1
		for i, v := range kept {
			if !v.pinned && v.t != latest {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		v := kept[idx]
		reason := ReasonOverBytes
		if count > c.maxCount {
			reason = ReasonOverCount
		}
		kept = append(kept[:idx], kept[idx+1:]...)
		count--
		bytes -= v.size
		remove(v, reason)
	}

	sort.Slice(deleted, func(i, j int) bool { return deleted[i].T < deleted[j].T })
	return deleted
}

// stepFor returns the Step of the tier containing the given age: the first
// rule whose Until strictly exceeds the age. Ages at or beyond the last
// Until are over-aged and never reach the Thinned phase as candidates, so
// the last Step is only a defensive fallback.
func (c *Cleaner) stepFor(age int64) int64 {
	for _, r := range c.rules {
		if age < r.Until {
			return r.Step
		}
	}
	return c.rules[len(c.rules)-1].Step
}

func (c *Cleaner) sortedFiles() []string {
	names := make([]string, 0, len(c.files))
	for name := range c.files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Cleaner) checkClock(now int64) error {
	if c.hasNow && now < c.lastNow {
		return ErrClock
	}
	return nil
}

func (c *Cleaner) acceptNow(now int64) {
	c.hasNow = true
	c.lastNow = now
}

// Undelete restores a trashed version whose trash TTL has not elapsed.
// The restored version comes back unpinned. An entry whose TTL has elapsed
// but which Clean has not purged yet is treated as absent.
func (c *Cleaner) Undelete(now int64, file string, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if now < 0 {
		return ErrInvalid
	}
	fs := c.files[file]
	if fs == nil {
		return ErrNoVersion
	}
	v, ok := fs.versions[t]
	if !ok || !v.deleted || now-v.deletedAt >= c.trashTTL {
		return ErrNoVersion
	}
	v.deleted = false
	v.pinned = false
	c.acceptNow(now)
	return nil
}

// Versions returns the active versions of file, ordered by t ascending.
// Unknown files yield an empty result, not an error.
func (c *Cleaner) Versions(file string) []Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.files[file]
	if fs == nil {
		return nil
	}
	active := fs.active()
	out := make([]Version, len(active))
	for i, v := range active {
		out[i] = Version{T: v.t, Size: v.size, Pinned: v.pinned}
	}
	return out
}

// Totals returns the count and summed size of the active versions of file.
func (c *Cleaner) Totals(file string) (count int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.files[file]
	if fs == nil {
		return 0, 0
	}
	for _, v := range fs.versions {
		if !v.deleted {
			count++
			bytes += v.size
		}
	}
	return count, bytes
}

// Trash returns the trash entries of file, ordered by t ascending.
func (c *Cleaner) Trash(file string) []TrashEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	fs := c.files[file]
	if fs == nil {
		return nil
	}
	trashed := fs.trashed()
	out := make([]TrashEntry, len(trashed))
	for i, v := range trashed {
		out[i] = TrashEntry{T: v.t, Size: v.size, DeletedAt: v.deletedAt}
	}
	return out
}
