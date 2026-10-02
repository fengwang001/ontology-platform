package compaction

import (
	"bytes"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// naivePicker is a deliberately literal, step-by-step transcription of the
// picking rules, used to cross-check the real Picker. It favors clarity over
// efficiency: linear scans, big.Rat score arithmetic, and set comparisons
// that do not rely on files being sorted.
type naivePicker struct {
	L     int
	caps  []int64
	x     int64
	files map[int][]File
	ptrs  map[int][]byte // nil means "none"
	ids   map[uint64]bool
}

func newNaive(L int, caps []int64, x int64) *naivePicker {
	return &naivePicker{
		L:     L,
		caps:  append([]int64(nil), caps...),
		x:     x,
		files: map[int][]File{},
		ptrs:  map[int][]byte{},
		ids:   map[uint64]bool{},
	}
}

func naiveOverlap(aMin, aMax, bMin, bMax []byte) bool {
	return bytes.Compare(aMin, bMax) <= 0 && bytes.Compare(bMin, aMax) <= 0
}

func (n *naivePicker) add(f File) error {
	if f.Level < 1 || f.Level > n.L {
		return ErrLevelOutOfRange
	}
	if bytes.Compare(f.MinKey, f.MaxKey) > 0 {
		return ErrInvalidKeyRange
	}
	if f.Size <= 0 {
		return ErrNonPositiveSize
	}
	if n.ids[f.ID] {
		return ErrDuplicateFileID
	}
	for _, g := range n.files[f.Level] {
		if naiveOverlap(f.MinKey, f.MaxKey, g.MinKey, g.MaxKey) {
			return ErrOverlappingFile
		}
	}
	n.files[f.Level] = append(n.files[f.Level], f)
	sort.Slice(n.files[f.Level], func(a, b int) bool {
		return bytes.Compare(n.files[f.Level][a].MinKey, n.files[f.Level][b].MinKey) < 0
	})
	n.ids[f.ID] = true
	return nil
}

func (n *naivePicker) pick(t *testing.T) (inputs, overlaps []File, err error) {
	// Step 1: score every level 1..L-1 as bytes/cap with exact rationals.
	bestLevel := -1
	var bestScore *big.Rat
	var basis []string
	for i := 1; i <= n.L-1; i++ {
		var sum int64
		for _, f := range n.files[i] {
			sum += f.Size
		}
		score := new(big.Rat).SetFrac(big.NewInt(sum), big.NewInt(n.caps[i-1]))
		basis = append(basis, fmt.Sprintf("level %d: bytes=%d cap=%d score=%s", i, sum, n.caps[i-1], score.RatString()))
		if bestLevel < 0 || score.Cmp(bestScore) > 0 {
			bestLevel, bestScore = i, score
		}
	}
	one := big.NewRat(1, 1)
	basis = append(basis, fmt.Sprintf("max score=%s at level %d", bestScore.RatString(), bestLevel))
	if bestScore.Cmp(one) < 0 {
		t.Logf("naive pick basis: %s -> nothing to do", strings.Join(basis, "; "))
		return nil, nil, ErrNothingToCompact
	}

	// Step 2: start file.
	levelFiles := n.files[bestLevel]
	ptr := n.ptrs[bestLevel]
	startIdx := 0
	if ptr != nil {
		startIdx = -1
		for i, f := range levelFiles {
			if bytes.Compare(f.MinKey, ptr) > 0 {
				startIdx = i
				break
			}
		}
		if startIdx < 0 {
			startIdx = 0
			basis = append(basis, "no min key > ptr, wrapped")
		}
	}
	start := levelFiles[startIdx]
	basis = append(basis, fmt.Sprintf("ptr=%q start=file %d [%q,%q]", ptr, start.ID, start.MinKey, start.MaxKey))

	// Step 3: overlap set O in level bestLevel+1.
	for _, f := range n.files[bestLevel+1] {
		if naiveOverlap(f.MinKey, f.MaxKey, start.MinKey, start.MaxKey) {
			overlaps = append(overlaps, f)
		}
	}

	// Step 4: expansion.
	rMin, rMax := start.MinKey, start.MaxKey
	for _, f := range overlaps {
		if bytes.Compare(f.MinKey, rMin) < 0 {
			rMin = f.MinKey
		}
		if bytes.Compare(f.MaxKey, rMax) > 0 {
			rMax = f.MaxKey
		}
	}
	var tset []File
	for _, f := range levelFiles {
		if naiveOverlap(f.MinKey, f.MaxKey, rMin, rMax) {
			tset = append(tset, f)
		}
	}
	expand := false
	if len(tset) <= 1 {
		basis = append(basis, "no expansion: T has only the start file")
	} else {
		var sum int64
		for _, f := range tset {
			sum += f.Size
		}
		for _, f := range overlaps {
			sum += f.Size
		}
		if sum >= n.x {
			basis = append(basis, fmt.Sprintf("no expansion: bytes(T)+bytes(O)=%d >= X=%d", sum, n.x))
		} else {
			// Hull of T computed without relying on sort order.
			hMin, hMax := tset[0].MinKey, tset[0].MaxKey
			for _, f := range tset[1:] {
				if bytes.Compare(f.MinKey, hMin) < 0 {
					hMin = f.MinKey
				}
				if bytes.Compare(f.MaxKey, hMax) > 0 {
					hMax = f.MaxKey
				}
			}
			grown := map[uint64]bool{}
			for _, f := range n.files[bestLevel+1] {
				if naiveOverlap(f.MinKey, f.MaxKey, hMin, hMax) {
					grown[f.ID] = true
				}
			}
			if len(grown) != len(overlaps) {
				basis = append(basis, "no expansion: overlap set would grow")
			} else {
				same := true
				for _, f := range overlaps {
					if !grown[f.ID] {
						same = false
					}
				}
				if !same {
					basis = append(basis, "no expansion: overlap set would grow")
				} else {
					expand = true
					basis = append(basis, fmt.Sprintf("expanded: T=%d files, sum=%d < X=%d", len(tset), sum, n.x))
				}
			}
		}
	}
	if expand {
		inputs = tset
	} else {
		inputs = []File{start}
	}

	// Step 5: advance the pointer to the largest min key among inputs.
	maxMin := inputs[0].MinKey
	for _, f := range inputs[1:] {
		if bytes.Compare(f.MinKey, maxMin) > 0 {
			maxMin = f.MinKey
		}
	}
	n.ptrs[bestLevel] = append([]byte(nil), maxMin...)
	basis = append(basis, fmt.Sprintf("ptr[%d] <- %q", bestLevel, maxMin))

	t.Logf("naive pick basis: %s", strings.Join(basis, "; "))
	t.Logf("naive pick result: inputs=%v overlaps=%v", idsOf(inputs), idsOf(overlaps))
	return inputs, overlaps, nil
}

// sentinelOf maps an error to its sentinel for comparison.
func sentinelOf(err error) error {
	if err == nil {
		return nil
	}
	for _, s := range []error{
		ErrLevelOutOfRange, ErrInvalidKeyRange, ErrNonPositiveSize,
		ErrDuplicateFileID, ErrOverlappingFile, ErrNothingToCompact,
	} {
		if errors.Is(err, s) {
			return s
		}
	}
	return fmt.Errorf("unknown error: %v", err)
}

func randomKey(rng *rand.Rand) []byte {
	n := 1 + rng.Intn(2)
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + rng.Intn(8))
	}
	return b
}

func TestDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			L := 2 + rng.Intn(3)
			caps := make([]int64, L-1)
			for i := range caps {
				caps[i] = int64(1 + rng.Intn(60))
			}
			x := int64(1 + rng.Intn(200))
			t.Logf("config: L=%d caps=%v X=%d", L, caps, x)

			real, err := NewPicker(L, caps, x)
			if err != nil {
				t.Fatal(err)
			}
			naive := newNaive(L, caps, x)

			for op := 0; op < 300; op++ {
				if rng.Intn(100) < 60 {
					// Random AddFile, sometimes deliberately invalid.
					f := File{
						ID:     uint64(1 + rng.Intn(40)),
						Level:  rng.Intn(L + 2), // may be 0 or L+1
						MinKey: randomKey(rng),
						MaxKey: randomKey(rng),
						Size:   int64(rng.Intn(45) - 2), // may be <= 0
					}
					if rng.Intn(100) < 80 && bytes.Compare(f.MinKey, f.MaxKey) > 0 {
						f.MinKey, f.MaxKey = f.MaxKey, f.MinKey
					}
					errReal := real.AddFile(f)
					errNaive := naive.add(f)
					if sentinelOf(errReal) != sentinelOf(errNaive) {
						t.Fatalf("op %d AddFile(%+v): real=%v naive=%v", op, f, errReal, errNaive)
					}
					t.Logf("op %d AddFile(id=%d lvl=%d [%q,%q] sz=%d) -> %v",
						op, f.ID, f.Level, f.MinKey, f.MaxKey, f.Size, sentinelOf(errReal))
				} else {
					inR, ovR, errR := real.Pick()
					inN, ovN, errN := naive.pick(t)
					if sentinelOf(errR) != sentinelOf(errN) {
						t.Fatalf("op %d Pick: real err=%v naive err=%v", op, errR, errN)
					}
					if errR == nil {
						if !reflect.DeepEqual(idsOf(inR), idsOf(inN)) {
							t.Fatalf("op %d Pick inputs: real=%v naive=%v", op, idsOf(inR), idsOf(inN))
						}
						if !reflect.DeepEqual(idsOf(ovR), idsOf(ovN)) {
							t.Fatalf("op %d Pick overlaps: real=%v naive=%v", op, idsOf(ovR), idsOf(ovN))
						}
					}
				}

				// Pointers must match after every operation.
				for lvl := 1; lvl <= L; lvl++ {
					key, set, err := real.Ptr(lvl)
					if err != nil {
						t.Fatal(err)
					}
					nptr := naive.ptrs[lvl]
					if set != (nptr != nil) || (set && !bytes.Equal(key, nptr)) {
						t.Fatalf("op %d ptr[%d]: real=(%q,%v) naive=(%q,%v)",
							op, lvl, key, set, nptr, nptr != nil)
					}
				}
			}
		})
	}
}

func TestReplayDeterminism(t *testing.T) {
	run := func() [][]uint64 {
		p := mustNew(t, 3, []int64{50, 80}, 120)
		rng := rand.New(rand.NewSource(42))
		var log [][]uint64
		id := uint64(1)
		for op := 0; op < 120; op++ {
			if rng.Intn(100) < 55 {
				f := File{ID: id, Level: 1 + rng.Intn(3), MinKey: randomKey(rng), MaxKey: randomKey(rng), Size: int64(1 + rng.Intn(40))}
				if bytes.Compare(f.MinKey, f.MaxKey) > 0 {
					f.MinKey, f.MaxKey = f.MaxKey, f.MinKey
				}
				if p.AddFile(f) == nil {
					id++
				}
			} else {
				inputs, overlaps, err := p.Pick()
				if err == nil {
					log = append(log, append(idsOf(inputs), idsOf(overlaps)...))
				}
			}
		}
		return log
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replaying the same call sequence diverged:\n%v\n%v", first, second)
	}
}

func TestConcurrentAccess(t *testing.T) {
	p := mustNew(t, 3, []int64{40, 90}, 150)
	done := make(chan struct{})
	var wg sync.WaitGroup

	// Writers: disjoint key ranges per worker keep adds valid.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w)))
			for i := 0; i < 60; i++ {
				select {
				case <-done:
					return
				default:
				}
				lo := byte('a' + w*2)
				f := File{
					ID:     uint64(w*1000 + i + 1),
					Level:  1 + rng.Intn(3),
					MinKey: []byte{lo, byte(i)},
					MaxKey: []byte{lo, byte(i)},
					Size:   int64(1 + rng.Intn(30)),
				}
				_ = p.AddFile(f) // rejections (e.g. overlap) are fine
			}
		}(w)
	}
	// Pickers and readers.
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 120; i++ {
				_, _, _ = p.Pick()
				for lvl := 1; lvl <= 3; lvl++ {
					_, _ = p.Files(lvl)
					_, _, _ = p.Ptr(lvl)
				}
			}
		}()
	}
	wg.Wait()
	close(done)

	// Invariants: every level sorted by min key and pairwise disjoint.
	for lvl := 1; lvl <= 3; lvl++ {
		files, err := p.Files(lvl)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(files); i++ {
			if bytes.Compare(files[i-1].MinKey, files[i].MinKey) >= 0 {
				t.Fatalf("level %d not sorted at %d", lvl, i)
			}
			if bytes.Compare(files[i-1].MaxKey, files[i].MinKey) >= 0 {
				t.Fatalf("level %d files %d and %d overlap", lvl, files[i-1].ID, files[i].ID)
			}
		}
	}
}
