// Package compaction implements a deterministic tiered compaction input
// picker. It scores levels, picks a start file via a round-robin pointer,
// computes the next-level overlap set, and optionally expands the
// same-level input set. All results and pointer advances are exactly
// reproducible for a given call sequence.
package compaction

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"sync"
)

// Distinguishable rejection reasons. Use errors.Is to match them.
var (
	// Constructor rejections.
	ErrInvalidLevelCount = errors.New("compaction: level count L must be >= 2")
	ErrCapCountMismatch  = errors.New("compaction: caps must contain exactly L-1 entries")
	ErrNonPositiveCap    = errors.New("compaction: capacity must be a positive byte count")
	ErrNonPositiveX      = errors.New("compaction: expansion limit X must be positive")

	// AddFile rejections, reported in this exact order.
	ErrLevelOutOfRange = errors.New("compaction: file level out of range [1, L]")
	ErrInvalidKeyRange = errors.New("compaction: file min key greater than max key")
	ErrNonPositiveSize = errors.New("compaction: file size must be positive")
	ErrDuplicateFileID = errors.New("compaction: duplicate file id")
	ErrOverlappingFile = errors.New("compaction: file overlaps an existing file in the same level")

	// Pick rejection.
	ErrNothingToCompact = errors.New("compaction: nothing to do, max level score below 1")
)

// File is a single sorted-run file. Keys are inclusive byte-string ranges.
type File struct {
	ID     uint64
	Level  int
	MinKey []byte
	MaxKey []byte
	Size   int64
}

// ptrState is a per-level round-robin pointer. "None" is set == false.
type ptrState struct {
	set bool
	key []byte
}

// Picker holds the full picker state. All methods are safe for concurrent
// use; results are equivalent to some serial execution order.
type Picker struct {
	mu     sync.Mutex
	L      int
	caps   []int64 // caps[i] is the capacity of level i+1; len == L-1
	x      int64
	levels [][]File // levels[i] holds level i+1 files sorted by MinKey
	ptrs   []ptrState
	ids    map[uint64]struct{}
}

// NewPicker validates the configuration and returns an empty picker.
func NewPicker(L int, caps []int64, x int64) (*Picker, error) {
	if L < 2 {
		return nil, ErrInvalidLevelCount
	}
	if len(caps) != L-1 {
		return nil, ErrCapCountMismatch
	}
	for _, c := range caps {
		if c <= 0 {
			return nil, ErrNonPositiveCap
		}
	}
	if x <= 0 {
		return nil, ErrNonPositiveX
	}
	p := &Picker{
		L:      L,
		caps:   append([]int64(nil), caps...),
		x:      x,
		levels: make([][]File, L),
		ptrs:   make([]ptrState, L),
		ids:    make(map[uint64]struct{}),
	}
	return p, nil
}

// AddFile validates and inserts a file. On any rejection the picker state
// (files and pointers) is left untouched. Validations run in the order:
// level range, key range, size, duplicate id, same-level overlap.
func (p *Picker) AddFile(f File) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if f.Level < 1 || f.Level > p.L {
		return fmt.Errorf("%w: got level %d with L=%d", ErrLevelOutOfRange, f.Level, p.L)
	}
	if bytes.Compare(f.MinKey, f.MaxKey) > 0 {
		return fmt.Errorf("%w: file %d min %q > max %q", ErrInvalidKeyRange, f.ID, f.MinKey, f.MaxKey)
	}
	if f.Size <= 0 {
		return fmt.Errorf("%w: file %d has size %d", ErrNonPositiveSize, f.ID, f.Size)
	}
	if _, dup := p.ids[f.ID]; dup {
		return fmt.Errorf("%w: file id %d already exists", ErrDuplicateFileID, f.ID)
	}
	files := p.levels[f.Level-1]
	for _, g := range files {
		if rangesOverlap(f.MinKey, f.MaxKey, g.MinKey, g.MaxKey) {
			return fmt.Errorf("%w: file %d [%q,%q] overlaps file %d [%q,%q] in level %d",
				ErrOverlappingFile, f.ID, f.MinKey, f.MaxKey, g.ID, g.MinKey, g.MaxKey, f.Level)
		}
	}

	stored := File{
		ID:     f.ID,
		Level:  f.Level,
		MinKey: append([]byte(nil), f.MinKey...),
		MaxKey: append([]byte(nil), f.MaxKey...),
		Size:   f.Size,
	}
	pos := 0
	for pos < len(files) && bytes.Compare(files[pos].MinKey, stored.MinKey) < 0 {
		pos++
	}
	files = append(files, File{})
	copy(files[pos+1:], files[pos:])
	files[pos] = stored
	p.levels[f.Level-1] = files
	p.ids[f.ID] = struct{}{}
	return nil
}

// Pick selects one compaction: it scores levels 1..L-1, picks the start
// file, computes the next-level overlap set, applies the expansion rule,
// and advances the round-robin pointer of the picked level.
func (p *Picker) Pick() (inputs []File, overlaps []File, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	level := p.bestLevelLocked()
	if level < 0 {
		return nil, nil, ErrNothingToCompact
	}
	idx := level - 1
	files := p.levels[idx]

	start := startIndex(files, p.ptrs[idx])
	seed := files[start]

	next := p.levels[idx+1]
	overlaps = filterOverlapping(next, seed.MinKey, seed.MaxKey)

	// R is the smallest closed interval covering the start file and O.
	rMin, rMax := seed.MinKey, seed.MaxKey
	for _, f := range overlaps {
		if bytes.Compare(f.MinKey, rMin) < 0 {
			rMin = f.MinKey
		}
		if bytes.Compare(f.MaxKey, rMax) > 0 {
			rMax = f.MaxKey
		}
	}

	// T: same-level files intersecting R (contains the start file).
	tset := filterOverlapping(files, rMin, rMax)

	inputs = []File{seed}
	if len(tset) > 1 && totalSize(tset)+totalSize(overlaps) < p.x {
		// The union range of T is the hull of its disjoint, sorted files.
		tMin, tMax := tset[0].MinKey, tset[len(tset)-1].MaxKey
		grown := filterOverlapping(next, tMin, tMax)
		if sameFileSet(grown, overlaps) {
			inputs = tset
		}
	}

	// Advance the pointer to the largest min key among the inputs.
	last := inputs[len(inputs)-1]
	p.ptrs[idx] = ptrState{set: true, key: append([]byte(nil), last.MinKey...)}

	return cloneFiles(inputs), cloneFiles(overlaps), nil
}

// bestLevelLocked returns the level (1-based, in [1, L-1]) with the highest
// score bytes/cap, breaking ties toward the smaller level number, or -1 when
// the maximum score is below 1. Scores are compared by exact
// cross-multiplication with big integers.
func (p *Picker) bestLevelLocked() int {
	best := -1
	var bestBytes, bestCap int64
	for i := 0; i < p.L-1; i++ {
		var bytesSum int64
		for _, f := range p.levels[i] {
			bytesSum += f.Size
		}
		if best < 0 || greaterScore(bytesSum, p.caps[i], bestBytes, bestCap) {
			best = i
			bestBytes, bestCap = bytesSum, p.caps[i]
		}
	}
	// Score >= 1 iff bytes >= cap; exactly 1 is compactable.
	if bestBytes < bestCap {
		return -1
	}
	return best + 1
}

// greaterScore reports whether a/b > c/d exactly, for positive b and d.
func greaterScore(a, b, c, d int64) bool {
	left := new(big.Int).Mul(big.NewInt(a), big.NewInt(d))
	right := new(big.Int).Mul(big.NewInt(c), big.NewInt(b))
	return left.Cmp(right) > 0
}

// startIndex finds the start file: the first file whose min key is strictly
// greater than the pointer, wrapping to the first file when there is none.
// A "none" pointer selects the first file.
func startIndex(files []File, ptr ptrState) int {
	if !ptr.set {
		return 0
	}
	for i, f := range files {
		if bytes.Compare(f.MinKey, ptr.key) > 0 {
			return i
		}
	}
	return 0
}

func filterOverlapping(files []File, min, max []byte) []File {
	var out []File
	for _, f := range files {
		if rangesOverlap(f.MinKey, f.MaxKey, min, max) {
			out = append(out, f)
		}
	}
	return out
}

func totalSize(files []File) int64 {
	var sum int64
	for _, f := range files {
		sum += f.Size
	}
	return sum
}

func sameFileSet(a, b []File) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

// Files returns a copy of the files of the given level, sorted by MinKey.
func (p *Picker) Files(level int) ([]File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if level < 1 || level > p.L {
		return nil, fmt.Errorf("%w: got level %d with L=%d", ErrLevelOutOfRange, level, p.L)
	}
	return cloneFiles(p.levels[level-1]), nil
}

// Ptr returns the round-robin pointer of the given level; set is false when
// the pointer is still "none".
func (p *Picker) Ptr(level int) (key []byte, set bool, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if level < 1 || level > p.L {
		return nil, false, fmt.Errorf("%w: got level %d with L=%d", ErrLevelOutOfRange, level, p.L)
	}
	ptr := p.ptrs[level-1]
	if !ptr.set {
		return nil, false, nil
	}
	return append([]byte(nil), ptr.key...), true, nil
}

// rangesOverlap reports whether inclusive ranges [aMin,aMax] and [bMin,bMax]
// intersect. Equal endpoints count as intersecting.
func rangesOverlap(aMin, aMax, bMin, bMax []byte) bool {
	return bytes.Compare(aMin, bMax) <= 0 && bytes.Compare(bMin, aMax) <= 0
}

func cloneFiles(in []File) []File {
	out := make([]File, len(in))
	for i, f := range in {
		out[i] = File{
			ID:     f.ID,
			Level:  f.Level,
			MinKey: append([]byte(nil), f.MinKey...),
			MaxKey: append([]byte(nil), f.MaxKey...),
			Size:   f.Size,
		}
	}
	return out
}
