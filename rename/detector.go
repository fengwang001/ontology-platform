// Package rename implements a deterministic two-phase file rename detector.
package rename

import (
	"errors"
	"sort"
	"sync"
)

// Sentinel errors returned by the detector. They are intentionally distinct so
// callers (and tests) can identify the exact rejection reason.
var (
	// ErrThresholdOutOfRange is returned by NewDetector when T is not in [1,100].
	ErrThresholdOutOfRange = errors.New("rename: threshold must be between 1 and 100")
	// ErrCapOutOfRange is returned by NewDetector when Cap < 1.
	ErrCapOutOfRange = errors.New("rename: capacity must be at least 1")
	// ErrFrozen is returned by Register* once Detect has frozen the session.
	ErrFrozen = errors.New("rename: session is frozen")
	// ErrEmptyPath is returned when the registered path is empty.
	ErrEmptyPath = errors.New("rename: path is empty")
	// ErrPathExists is returned when the path was already registered on either side.
	ErrPathExists = errors.New("rename: path already registered")
	// ErrCapacityExceeded is returned when the registration cap is reached.
	ErrCapacityExceeded = errors.New("rename: registration capacity exceeded")
)

// Rename is one matched (source, target) pair with its similarity score.
type Rename struct {
	Source string
	Target string
	Score  int
}

// Result is the frozen detection outcome. Slice fields are never nil.
type Result struct {
	Renames         []Rename
	UnpairedDeleted []string
	UnpairedAdded   []string
}

// Detector collects deleted and added files for one change, then detects
// renames exactly once. A Detector is safe for concurrent use.
type Detector struct {
	threshold int
	capacity  int

	mu sync.Mutex

	deletes []fileEntry
	adds    []fileEntry
	known   map[string]bool

	// detectCalls counts Detect invocations (including concurrent/repeated
	// calls); computeCalls counts how many times the pairing result was
	// actually computed (must be at most one per Detector).
	detectCalls  int
	computeCalls int
	// commonBytesCalls counts invocations of the common-bytes computation;
	// it is used to prove the size-ratio pruning.
	commonBytesCalls int

	result *Result
	done   bool
}

type fileEntry struct {
	path    string
	content []byte
}

// NewDetector creates a detector with similarity threshold T (1..100) and a
// total registration cap Cap (>= 1). Threshold is validated before capacity.
func NewDetector(threshold, capacity int) (*Detector, error) {
	if threshold < 1 || threshold > 100 {
		return nil, ErrThresholdOutOfRange
	}
	if capacity < 1 {
		return nil, ErrCapOutOfRange
	}
	return &Detector{
		threshold: threshold,
		capacity:  capacity,
		known:     make(map[string]bool),
	}, nil
}

// RegisterDeleted records a deleted file.
func (d *Detector) RegisterDeleted(path string, content []byte) error {
	return d.register(true, path, content)
}

// RegisterAdded records an added file.
func (d *Detector) RegisterAdded(path string, content []byte) error {
	return d.register(false, path, content)
}

func (d *Detector) register(deleted bool, path string, content []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Error precedence: frozen, empty path, duplicate path, cap reached.
	if d.done {
		return ErrFrozen
	}
	if path == "" {
		return ErrEmptyPath
	}
	if d.known[path] {
		return ErrPathExists
	}
	if len(d.deletes)+len(d.adds) >= d.capacity {
		return ErrCapacityExceeded
	}

	entry := fileEntry{path: path, content: append([]byte(nil), content...)}
	if deleted {
		d.deletes = append(d.deletes, entry)
	} else {
		d.adds = append(d.adds, entry)
	}
	d.known[path] = true
	return nil
}

// Detect computes the pairing result on first call, freezes the session, and
// returns the same result on every subsequent (including concurrent) call.
func (d *Detector) Detect() *Result {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.detectCalls++
	if d.done {
		return d.result
	}

	d.result = d.compute()
	d.computeCalls++
	d.done = true
	return d.result
}

// compute runs both pairing phases. It is invoked at most once per Detector.
func (d *Detector) compute() *Result {
	remainingDeletes := append([]fileEntry(nil), d.deletes...)
	remainingAdds := append([]fileEntry(nil), d.adds...)

	renames := []Rename{}
	exact := d.pairExact(&remainingDeletes, &remainingAdds)
	similar, leftoverDeletes, leftoverAdds := d.pairSimilar(remainingDeletes, remainingAdds)
	renames = append(renames, exact...)
	renames = append(renames, similar...)
	remainingDeletes = leftoverDeletes
	remainingAdds = leftoverAdds

	sort.Slice(renames, func(i, j int) bool {
		return renames[i].Source < renames[j].Source
	})

	unpairedDeletes := make([]string, 0, len(remainingDeletes))
	for _, entry := range remainingDeletes {
		unpairedDeletes = append(unpairedDeletes, entry.path)
	}
	sort.Strings(unpairedDeletes)

	unpairedAdds := make([]string, 0, len(remainingAdds))
	for _, entry := range remainingAdds {
		unpairedAdds = append(unpairedAdds, entry.path)
	}
	sort.Strings(unpairedAdds)

	return &Result{
		Renames:         renames,
		UnpairedDeleted: unpairedDeletes,
		UnpairedAdded:   unpairedAdds,
	}
}

type exactGroup struct {
	deletes []fileEntry
	adds    []fileEntry
}

// pairExact groups remaining files by byte-identical content. Within each
// content group, files sharing a basename pair first, then leftovers pair
// across basenames. Pairing order inside every sub-group is full path order.
func (d *Detector) pairExact(deletes, adds *[]fileEntry) []Rename {
	groups := make(map[string]*exactGroup)
	for _, entry := range *deletes {
		g := groups[string(entry.content)]
		if g == nil {
			g = &exactGroup{}
			groups[string(entry.content)] = g
		}
		g.deletes = append(g.deletes, entry)
	}
	for _, entry := range *adds {
		g := groups[string(entry.content)]
		if g == nil {
			g = &exactGroup{}
			groups[string(entry.content)] = g
		}
		g.adds = append(g.adds, entry)
	}

	renames := []Rename{}
	matchedDeletes := make(map[string]bool)
	matchedAdds := make(map[string]bool)

	record := func(del, add fileEntry) {
		renames = append(renames, Rename{Source: del.path, Target: add.path, Score: 100})
		matchedDeletes[del.path] = true
		matchedAdds[add.path] = true
	}

	for _, g := range groups {
		// Sub-groups by basename; a sub-group only pairs when both sides exist.
		subs := make(map[string]*exactGroup)
		for _, entry := range g.deletes {
			key := basename(entry.path)
			if subs[key] == nil {
				subs[key] = &exactGroup{}
			}
			subs[key].deletes = append(subs[key].deletes, entry)
		}
		for _, entry := range g.adds {
			key := basename(entry.path)
			if subs[key] == nil {
				subs[key] = &exactGroup{}
			}
			subs[key].adds = append(subs[key].adds, entry)
		}

		names := make([]string, 0, len(subs))
		for name := range subs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sub := subs[name]
			if len(sub.deletes) == 0 || len(sub.adds) == 0 {
				continue
			}
			sortByPath(sub.deletes)
			sortByPath(sub.adds)
			n := min(len(sub.deletes), len(sub.adds))
			for i := 0; i < n; i++ {
				record(sub.deletes[i], sub.adds[i])
			}
		}

		var leftoverDeletes, leftoverAdds []fileEntry
		for _, entry := range g.deletes {
			if !matchedDeletes[entry.path] {
				leftoverDeletes = append(leftoverDeletes, entry)
			}
		}
		for _, entry := range g.adds {
			if !matchedAdds[entry.path] {
				leftoverAdds = append(leftoverAdds, entry)
			}
		}
		sortByPath(leftoverDeletes)
		sortByPath(leftoverAdds)
		n := min(len(leftoverDeletes), len(leftoverAdds))
		for i := 0; i < n; i++ {
			record(leftoverDeletes[i], leftoverAdds[i])
		}
	}

	removeMatched(deletes, matchedDeletes)
	removeMatched(adds, matchedAdds)
	return renames
}

type candidate struct {
	delIndex int
	addIndex int
	score    int
}

// pairSimilar scores every remaining delete/add combination that survives the
// size-ratio pruning, then greedily pairs in strict order
// (score desc, same basename first, delete path asc, add path asc). It
// returns the renames together with the files left unpaired on each side.
func (d *Detector) pairSimilar(deletes, adds []fileEntry) ([]Rename, []fileEntry, []fileEntry) {
	var candidates []candidate

	for i, del := range deletes {
		for j, add := range adds {
			maxSize := max(len(del.content), len(add.content))
			minSize := min(len(del.content), len(add.content))
			// s = floor(C*100/max) cannot exceed floor(min*100/max);
			// pairs whose upper bound is already below T never need C.
			if maxSize == 0 || minSize*100/maxSize < d.threshold {
				continue
			}
			d.commonBytesCalls++
			common := commonBytes(del.content, add.content)
			score := common * 100 / maxSize
			if score >= d.threshold {
				candidates = append(candidates, candidate{delIndex: i, addIndex: j, score: score})
			}
		}
	}

	sort.SliceStable(candidates, func(a, b int) bool {
		ca, cb := candidates[a], candidates[b]
		if ca.score != cb.score {
			return ca.score > cb.score
		}
		sameA := basename(deletes[ca.delIndex].path) == basename(adds[ca.addIndex].path)
		sameB := basename(deletes[cb.delIndex].path) == basename(adds[cb.addIndex].path)
		if sameA != sameB {
			return sameA
		}
		pa := deletes[ca.delIndex].path
		pb := deletes[cb.delIndex].path
		if pa != pb {
			return pa < pb
		}
		return adds[ca.addIndex].path < adds[cb.addIndex].path
	})

	delTaken := make([]bool, len(deletes))
	addTaken := make([]bool, len(adds))
	renames := []Rename{}
	for _, cand := range candidates {
		if delTaken[cand.delIndex] || addTaken[cand.addIndex] {
			continue
		}
		delTaken[cand.delIndex] = true
		addTaken[cand.addIndex] = true
		renames = append(renames, Rename{
			Source: deletes[cand.delIndex].path,
			Target: adds[cand.addIndex].path,
			Score:  cand.score,
		})
	}

	leftoverDeletes := make([]fileEntry, 0, len(deletes))
	for i, entry := range deletes {
		if !delTaken[i] {
			leftoverDeletes = append(leftoverDeletes, entry)
		}
	}
	leftoverAdds := make([]fileEntry, 0, len(adds))
	for i, entry := range adds {
		if !addTaken[i] {
			leftoverAdds = append(leftoverAdds, entry)
		}
	}
	return renames, leftoverDeletes, leftoverAdds
}
