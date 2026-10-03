// Package ontology provides a tiered historical-version retention cleaner.
package ontology

import (
	"errors"
	"sort"
	"sync"
)

// Rule defines one retention tier: versions whose age is below Until belong to
// this tier (a version whose age equals Until belongs to the next tier) and use
// Step as the minimum spacing, in t units, during thinning.
type Rule struct {
	Until int64
	Step  int64
}

// Sentinel errors returned by the cleaner.
var (
	ErrClock     = errors.New("ontology: clock moved backwards")
	ErrInvalid   = errors.New("ontology: invalid argument")
	ErrDuplicate = errors.New("ontology: duplicate version")
	ErrNoVersion = errors.New("ontology: version not found")
	ErrTooLarge  = errors.New("ontology: total size would exceed the limit")
)

// Version describes one live version of a file.
type Version struct {
	T      int64
	Size   int64
	Pinned bool
}

// DeleteReason explains why a version was deleted by Clean.
type DeleteReason string

// Delete reasons.
const (
	ReasonAged      DeleteReason = "aged"
	ReasonThinned   DeleteReason = "thinned"
	ReasonOverCount DeleteReason = "over_count"
	ReasonOverBytes DeleteReason = "over_bytes"
)

// Deletion records a version removed by Clean together with the reason.
type Deletion struct {
	File   string
	T      int64
	Reason DeleteReason
}

// PurgedVersion records a trash entry permanently removed at Clean time.
type PurgedVersion struct {
	File string
	T    int64
}

// CleanResult is the outcome of one Clean call: trash entries permanently
// purged (ordered by file then t) and live versions moved to the trash
// (ordered by file then t).
type CleanResult struct {
	Purged  []PurgedVersion
	Deleted []Deletion
}

// TrashItem describes a version currently held in the recycle bin.
type TrashItem struct {
	T         int64
	Size      int64
	DeletedAt int64
}

type entry struct {
	t      int64
	size   int64
	pinned bool
}

type trashEntry struct {
	file      string
	t         int64
	size      int64
	deletedAt int64
}

const maxTotalBytes = int64(1_000_000_000_000_000)

// Cleaner keeps tiered, size- and count-bounded historical versions per file.
// All methods are safe for concurrent use.
type Cleaner struct {
	mu       sync.Mutex
	lastNow  int64
	rules    []Rule
	maxCount int64
	maxBytes int64
	trashTTL int64

	live  map[string]map[int64]*entry
	trash map[string]map[int64]*trashEntry

	// thinnedExams counts versions examined exactly once during the most
	// recent Clean thinning pass (sum over files, survivors of the aged step).
	thinnedExams int
}

// New creates a Cleaner from the supplied configuration.
func New(rules []Rule, maxCount, maxBytes, trashTTL int64) (*Cleaner, error) {
	if len(rules) == 0 {
		return nil, ErrInvalid
	}
	for i, r := range rules {
		if r.Until < 1 || r.Step < 1 {
			return nil, ErrInvalid
		}
		if i > 0 && r.Until <= rules[i-1].Until {
			return nil, ErrInvalid
		}
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
		live:     make(map[string]map[int64]*entry),
		trash:    make(map[string]map[int64]*trashEntry),
	}, nil
}

// tierStep returns the thinning Step for age a, or ok=false when the age is at
// or beyond the last tier's Until (the version is over-aged).
func (c *Cleaner) tierStep(age int64) (step int64, ok bool) {
	for _, r := range c.rules {
		if age < r.Until {
			return r.Step, true
		}
	}
	return 0, false
}

func sortedLive(m map[int64]*entry) []*entry {
	out := make([]*entry, 0, len(m))
	for _, e := range m {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t < out[j].t })
	return out
}

// Add stores a new live version. Error precedence:
// ErrClock > ErrInvalid > ErrDuplicate > ErrTooLarge.
func (c *Cleaner) Add(now int64, file string, t, size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.lastNow {
		return ErrClock
	}
	if file == "" || t < 0 || t > now || size < 1 {
		return ErrInvalid
	}
	if tf := c.trash[file]; tf != nil {
		if _, exists := tf[t]; exists {
			return ErrDuplicate
		}
	}
	lf := c.live[file]
	if lf != nil {
		if _, exists := lf[t]; exists {
			return ErrDuplicate
		}
	}
	var sum int64
	if lf != nil {
		for _, e := range lf {
			sum += e.size
		}
	}
	if size > maxTotalBytes-sum {
		return ErrTooLarge
	}
	c.lastNow = now
	if lf == nil {
		lf = make(map[int64]*entry)
		c.live[file] = lf
	}
	lf[t] = &entry{t: t, size: size}
	return nil
}

// Pin marks a live version as pinned. Pinned versions survive every Clean
// step. Re-pinning is an idempotent success; trashed or missing versions yield
// ErrNoVersion.
func (c *Cleaner) Pin(file string, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if file == "" {
		return ErrInvalid
	}
	if lf := c.live[file]; lf != nil {
		if e, ok := lf[t]; ok {
			e.pinned = true
			return nil
		}
	}
	return ErrNoVersion
}

// Unpin removes the pinned mark from a live version. It is idempotent;
// trashed or missing versions yield ErrNoVersion.
func (c *Cleaner) Unpin(file string, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if file == "" {
		return ErrInvalid
	}
	if lf := c.live[file]; lf != nil {
		if e, ok := lf[t]; ok {
			e.pinned = false
			return nil
		}
	}
	return ErrNoVersion
}

// Clean purges expired trash, then applies aged, thinning and limit rules to
// every file independently. Repeating Clean with the same now changes
// nothing.
func (c *Cleaner) Clean(now int64) CleanResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.lastNow {
		return CleanResult{}
	}
	c.lastNow = now

	var purged []PurgedVersion

	// Permanently remove trash entries whose trash TTL has elapsed.
	purgedFiles := make([]string, 0, len(c.trash))
	for file := range c.trash {
		purgedFiles = append(purgedFiles, file)
	}
	sort.Strings(purgedFiles)
	for _, file := range purgedFiles {
		tf := c.trash[file]
		var filePurged []int64
		for t, te := range tf {
			if now-te.deletedAt >= c.trashTTL {
				filePurged = append(filePurged, t)
				delete(tf, t)
			}
		}
		sort.Slice(filePurged, func(i, j int) bool { return filePurged[i] < filePurged[j] })
		for _, t := range filePurged {
			purged = append(purged, PurgedVersion{File: file, T: t})
		}
		if len(tf) == 0 {
			delete(c.trash, file)
		}
	}

	var deletions []Deletion

	files := make([]string, 0, len(c.live))
	for file := range c.live {
		files = append(files, file)
	}
	sort.Strings(files)

	c.thinnedExams = 0

	for _, file := range files {
		lf := c.live[file]
		if len(lf) == 0 {
			continue
		}
		all := sortedLive(lf)
		latestT := all[len(all)-1].t

		// Step A (aged): unpinned, non-latest versions older than the last
		// tier's Until move to the trash.
		survivors := make([]*entry, 0, len(all))
		for _, e := range all {
			_, inTier := c.tierStep(now - e.t)
			if !inTier && !e.pinned && e.t != latestT {
				c.moveToTrash(file, e)
				deletions = append(deletions, Deletion{File: file, T: e.t, Reason: ReasonAged})
				continue
			}
			survivors = append(survivors, e)
		}

		// Step B (thinned): each survivor is examined exactly once. A version
		// is kept when pinned, latest, or the first candidate; otherwise its
		// tier's Step decides whether spacing from prev is sufficient.
		c.thinnedExams += len(survivors)
		kept := make([]*entry, 0, len(survivors))
		var prev *entry
		for _, e := range survivors {
			if e.pinned || e.t == latestT || prev == nil {
				kept = append(kept, e)
				prev = e
				continue
			}
			step, _ := c.tierStep(now - e.t)
			if e.t-prev.t < step {
				c.moveToTrash(file, e)
				deletions = append(deletions, Deletion{File: file, T: e.t, Reason: ReasonThinned})
				continue
			}
			kept = append(kept, e)
			prev = e
		}

		// Step C (limits): while count or bytes exceed the cap, repeatedly
		// remove the oldest unpinned, non-latest version. Pinned and latest
		// versions are never removed, even if the caps remain violated.
		var totalSize int64
		for _, e := range kept {
			totalSize += e.size
		}
		totalCount := int64(len(kept))
		for totalCount > c.maxCount || totalSize > c.maxBytes {
			var victim *entry
			for _, e := range kept {
				if !e.pinned && e.t != latestT {
					victim = e
					break
				}
			}
			if victim == nil {
				break
			}
			reason := ReasonOverBytes
			if totalCount > c.maxCount {
				reason = ReasonOverCount
			}
			c.moveToTrash(file, victim)
			deletions = append(deletions, Deletion{File: file, T: victim.t, Reason: reason})
			totalCount--
			totalSize -= victim.size
			idx := sort.Search(len(kept), func(i int) bool { return kept[i].t >= victim.t })
			if idx < len(kept) && kept[idx].t == victim.t {
				kept = append(kept[:idx], kept[idx+1:]...)
			}
		}

		if len(c.live[file]) == 0 {
			delete(c.live, file)
		}
	}

	sort.SliceStable(deletions, func(i, j int) bool {
		if deletions[i].File != deletions[j].File {
			return deletions[i].File < deletions[j].File
		}
		return deletions[i].T < deletions[j].T
	})
	return CleanResult{Purged: purged, Deleted: deletions}
}

func (c *Cleaner) moveToTrash(file string, e *entry) {
	delete(c.live[file], e.t)
	tf := c.trash[file]
	if tf == nil {
		tf = make(map[int64]*trashEntry)
		c.trash[file] = tf
	}
	tf[e.t] = &trashEntry{file: file, t: e.t, size: e.size, deletedAt: c.lastNow}
}

// Undelete restores a trashed version whose trash TTL has not elapsed. The
// restored version loses any pinned mark. An expired-but-not-yet-purged entry
// is treated as absent.
func (c *Cleaner) Undelete(now int64, file string, t int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now < c.lastNow {
		return ErrClock
	}
	if file == "" {
		return ErrInvalid
	}
	tf := c.trash[file]
	if tf == nil {
		return ErrNoVersion
	}
	te, ok := tf[t]
	if !ok || now-te.deletedAt >= c.trashTTL {
		return ErrNoVersion
	}
	c.lastNow = now
	delete(tf, t)
	if len(tf) == 0 {
		delete(c.trash, file)
	}
	lf := c.live[file]
	if lf == nil {
		lf = make(map[int64]*entry)
		c.live[file] = lf
	}
	lf[t] = &entry{t: t, size: te.size}
	return nil
}

// Trash returns all trash items of one file ordered by t.
func (c *Cleaner) Trash(file string) []TrashItem {
	c.mu.Lock()
	defer c.mu.Unlock()
	tf := c.trash[file]
	out := make([]TrashItem, 0, len(tf))
	for _, te := range tf {
		out = append(out, TrashItem{T: te.t, Size: te.size, DeletedAt: te.deletedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out
}

// Versions returns live versions of one file ordered by t.
func (c *Cleaner) Versions(file string) []Version {
	c.mu.Lock()
	defer c.mu.Unlock()
	lf := c.live[file]
	out := make([]Version, 0, len(lf))
	for _, e := range sortedLive(lf) {
		out = append(out, Version{T: e.t, Size: e.size, Pinned: e.pinned})
	}
	return out
}

// Totals returns the live version count and summed size for one file.
func (c *Cleaner) Totals(file string) (int64, int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var count, bytes int64
	for _, e := range c.live[file] {
		count++
		bytes += e.size
	}
	return count, bytes
}
