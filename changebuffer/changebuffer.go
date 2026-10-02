// Package changebuffer implements a change buffer with a free-space
// estimation bitmap for secondary-index pages that are not resident in
// the buffer pool.
package changebuffer

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// MaxPage is the largest legal page number (pages are 0..MaxPage).
const MaxPage = 1_000_000

// Kind identifies a buffered operation kind.
type Kind int

const (
	// Insert adds an entry or clears the delete mark of an existing entry.
	Insert Kind = iota + 1
	// DeleteMark marks an existing entry as deleted.
	DeleteMark
	// Purge removes an existing entry that carries the delete mark.
	Purge
)

func (k Kind) valid() bool { return k >= Insert && k <= Purge }

// Entry is a page entry as returned by View.
type Entry struct {
	Key    string
	Size   int64
	Marked bool
}

// QueuedOp is one buffered operation in arrival order.
type QueuedOp struct {
	Kind Kind
	Key  string
	Size int64
}

// Decision describes why an Op was buffered or force-merged.
type Decision int

const (
	// DecisionAppliedDirect means the page was in the pool and the op
	// was applied directly.
	DecisionAppliedDirect Decision = iota + 1
	// DecisionBuffered means the op was appended to the page queue.
	DecisionBuffered
	// DecisionForceMerged means the page was merged into the pool and
	// the op was then applied directly.
	DecisionForceMerged
)

var (
	// ErrInvalidArgument is returned for illegal constructor/op arguments.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrPageNotInPool is returned by Evict when the page is not resident.
	ErrPageNotInPool = errors.New("page not in pool")
	// ErrPageSpace is returned when an Insert is rejected for lack of space.
	ErrPageSpace = errors.New("page space insufficient")
)

// ChangeBuffer is the change-buffer model.
type ChangeBuffer struct {
	mu sync.Mutex

	s  int64 // page size in bytes
	kp int64 // max buffered ops per page
	g  int64 // global buffered-bytes budget

	// pages maps page number to its state; absent means empty & not resident.
	pages map[int]*pageState

	// globalBuf is sum of bufBytes over all non-resident pages.
	globalBuf int64
}

type entryState struct {
	size   int64
	marked bool
}

type pageState struct {
	inPool bool
	// entries is the on-page materialized state (nil when empty).
	entries map[string]*entryState
	used    int64
	// queue is the arrival-ordered buffered operation queue.
	queue []QueuedOp
	// bufBytes is the sum of Insert sizes in queue.
	bufBytes int64
}

// New validates S, Kp, G and constructs an empty change buffer.
func New(S, Kp int64, G int64) (*ChangeBuffer, error) {
	if S < 64 || S > 1_000_000 || Kp < 1 || Kp > 1_000_000 || G < 1 || G > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	return &ChangeBuffer{
		s:     S,
		kp:    Kp,
		g:     G,
		pages: make(map[int]*pageState),
	}, nil
}

func validPage(page int) bool { return page >= 0 && page <= MaxPage }

func (cb *ChangeBuffer) getPage(page int) *pageState {
	p, ok := cb.pages[page]
	if !ok {
		p = &pageState{}
		cb.pages[page] = p
	}
	return p
}

// bucketOf maps free space F to a bucket index 0..3.
//
//	32F < S -> 0, else 16F < S -> 1, else 8F < S -> 2, else 3.
func bucketOf(s, free int64) int {
	switch {
	case 32*free < s:
		return 0
	case 16*free < s:
		return 1
	case 8*free < s:
		return 2
	default:
		return 3
	}
}

// lowerBoundOf returns lb(bucket): 0, floor(S/32), floor(S/16), floor(S/8).
func lowerBoundOf(s int64, bucket int) int64 {
	switch bucket {
	case 0:
		return 0
	case 1:
		return s / 32
	case 2:
		return s / 16
	default:
		return s / 8
	}
}

// applyEntry applies one op to an entry map. It returns false only when an
// Insert needs space that the page does not have; in that case the map is
// left untouched. Semantics are shared by direct application and merging.
func applyEntry(entries map[string]*entryState, used *int64, free int64, kind Kind, key string, e int64) bool {
	switch kind {
	case Insert:
		if en, ok := entries[key]; ok {
			en.marked = false
			return true
		}
		if free < e {
			return false
		}
		entries[key] = &entryState{size: e}
		*used += e
		return true
	case DeleteMark:
		if en, ok := entries[key]; ok {
			en.marked = true
		}
		return true
	default: // Purge
		if en, ok := entries[key]; ok && en.marked {
			delete(entries, key)
			*used -= en.size
		}
		return true
	}
}

// Op applies or buffers one operation, returning the decision taken.
func (cb *ChangeBuffer) Op(page int, kind Kind, key string, e int64) (Decision, error) {
	if !validPage(page) || !kind.valid() || key == "" {
		return 0, ErrInvalidArgument
	}
	if (kind == Insert && (e < 1 || e > cb.s)) || (kind != Insert && e != 0) {
		return 0, ErrInvalidArgument
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()

	p := cb.getPage(page)

	// Resident page: apply directly.
	if p.inPool {
		if !applyEntry(p.entries, &p.used, cb.s-p.used, kind, key, e) {
			return 0, ErrPageSpace
		}
		return DecisionAppliedDirect, nil
	}

	// Non-resident page: buffer iff under the per-page queue cap and, for
	// Inserts, under both the free-space guarantee and the global budget.
	canBuffer := int64(len(p.queue)) < cb.kp
	if canBuffer && kind == Insert {
		free := cb.s - p.used
		lb := lowerBoundOf(cb.s, bucketOf(cb.s, free))
		if p.bufBytes+e > lb || cb.globalBuf+e > cb.g {
			canBuffer = false
		}
	}
	if canBuffer {
		p.queue = append(p.queue, QueuedOp{Kind: kind, Key: key, Size: e})
		if kind == Insert {
			p.bufBytes += e
			cb.globalBuf += e
		}
		return DecisionBuffered, nil
	}

	// Force merge: play the queue into a working copy; merging never fails on
	// space, so failure must leave page entries, queue and counters untouched.
	entries := make(map[string]*entryState, len(p.entries))
	for k, en := range p.entries {
		entries[k] = &entryState{size: en.size, marked: en.marked}
	}
	var used int64 = p.used
	for _, op := range p.queue {
		if !applyEntry(entries, &used, cb.s-used, op.Kind, op.Key, op.Size) {
			// Unreachable: buffered Inserts are admitted under the bucket
			// guarantee, so a merge never runs out of space. Keep state
			// untouched defensively.
			return 0, ErrPageSpace
		}
	}

	// Apply the current op to the merged working copy before committing.
	if !applyEntry(entries, &used, cb.s-used, kind, key, e) {
		// Rejected: the page stays non-resident with its queue and entries
		// unchanged; the working copy is discarded.
		return 0, ErrPageSpace
	}

	// Commit: page becomes resident, queue is released.
	p.entries = entries
	p.used = used
	cb.globalBuf -= p.bufBytes
	p.bufBytes = 0
	p.queue = nil
	p.inPool = true
	return DecisionForceMerged, nil
}

// Load merges a non-resident page into the pool.
func (cb *ChangeBuffer) Load(page int) error {
	if !validPage(page) {
		return ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	p := cb.getPage(page)
	if p.inPool {
		return nil
	}

	entries := make(map[string]*entryState, len(p.entries))
	for k, en := range p.entries {
		entries[k] = &entryState{size: en.size, marked: en.marked}
	}
	var used int64 = p.used
	for _, op := range p.queue {
		if !applyEntry(entries, &used, cb.s-used, op.Kind, op.Key, op.Size) {
			return ErrPageSpace // unreachable for well-formed queues
		}
	}

	p.entries = entries
	p.used = used
	cb.globalBuf -= p.bufBytes
	p.bufBytes = 0
	p.queue = nil
	p.inPool = true
	return nil
}

// Evict removes a resident page from the pool.
func (cb *ChangeBuffer) Evict(page int) error {
	if !validPage(page) {
		return ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	p := cb.getPage(page)
	if !p.inPool {
		return ErrPageNotInPool
	}
	p.inPool = false
	return nil
}

// View returns the logical entry list after applying the queue, sorted by key.
func (cb *ChangeBuffer) View(page int) ([]Entry, error) {
	if !validPage(page) {
		return nil, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	p := cb.getPage(page)

	entries := make(map[string]*entryState, len(p.entries))
	for k, en := range p.entries {
		entries[k] = &entryState{size: en.size, marked: en.marked}
	}
	var used int64 = p.used
	for _, op := range p.queue {
		if !applyEntry(entries, &used, cb.s-used, op.Kind, op.Key, op.Size) {
			return nil, ErrPageSpace // unreachable for well-formed queues
		}
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Entry, 0, len(keys))
	for _, k := range keys {
		en := entries[k]
		out = append(out, Entry{Key: k, Size: en.size, Marked: en.marked})
	}
	return out, nil
}

// Queue returns a copy of the buffered queue of a non-resident page.
func (cb *ChangeBuffer) Queue(page int) ([]QueuedOp, error) {
	if !validPage(page) {
		return nil, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()

	p := cb.getPage(page)
	out := make([]QueuedOp, len(p.queue))
	copy(out, p.queue)
	return out, nil
}

// InPool reports whether the page is resident.
func (cb *ChangeBuffer) InPool(page int) (bool, error) {
	if !validPage(page) {
		return false, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.getPage(page).inPool, nil
}

// Used returns the sum of entry sizes currently stored on the page.
func (cb *ChangeBuffer) Used(page int) (int64, error) {
	if !validPage(page) {
		return 0, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.getPage(page).used, nil
}

// BufBytes returns the sum of Insert sizes buffered for the page.
func (cb *ChangeBuffer) BufBytes(page int) (int64, error) {
	if !validPage(page) {
		return 0, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.getPage(page).bufBytes, nil
}

// GlobalBufBytes returns the sum of bufBytes over all non-resident pages.
func (cb *ChangeBuffer) GlobalBufBytes() int64 {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.globalBuf
}

// Bucket returns bucket(F) for page free space F = S-used.
func (cb *ChangeBuffer) Bucket(page int) (int, error) {
	if !validPage(page) {
		return 0, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	p := cb.getPage(page)
	return bucketOf(cb.s, cb.s-p.used), nil
}

// LowerBound returns lb(bucket) for the given bucket.
func (cb *ChangeBuffer) LowerBound(bucket int) (int64, error) {
	if bucket < 0 || bucket > 3 {
		return 0, ErrInvalidArgument
	}
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return lowerBoundOf(cb.s, bucket), nil
}

// CheckInvariants verifies all model invariants; used by tests.
func (cb *ChangeBuffer) CheckInvariants() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	var global int64
	for page, p := range cb.pages {
		var sum int64
		for _, en := range p.entries {
			sum += en.size
		}
		if sum != p.used {
			return fmt.Errorf("page %d: used %d != entry size sum %d", page, p.used, sum)
		}
		if p.used > cb.s {
			return fmt.Errorf("page %d: used %d > S %d", page, p.used, cb.s)
		}
		if p.inPool && len(p.queue) != 0 {
			return fmt.Errorf("page %d: resident but queue non-empty", page)
		}
		if !p.inPool {
			if int64(len(p.queue)) > cb.kp {
				return fmt.Errorf("page %d: queue len %d > Kp %d", page, len(p.queue), cb.kp)
			}
			var qb int64
			for _, op := range p.queue {
				if op.Kind == Insert {
					qb += op.Size
				}
			}
			if qb != p.bufBytes {
				return fmt.Errorf("page %d: bufBytes %d != queue insert sum %d", page, p.bufBytes, qb)
			}
			lb := lowerBoundOf(cb.s, bucketOf(cb.s, cb.s-p.used))
			if p.bufBytes > lb {
				return fmt.Errorf("page %d: bufBytes %d > lb %d", page, p.bufBytes, lb)
			}
			global += p.bufBytes
		}
	}
	if global != cb.globalBuf {
		return fmt.Errorf("globalBuf %d != sum %d", cb.globalBuf, global)
	}
	if global > cb.g {
		return fmt.Errorf("globalBuf %d > G %d", global, cb.g)
	}
	return nil
}
