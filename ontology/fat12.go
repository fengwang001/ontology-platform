package ontology

import (
	"errors"
	"sync"
)

const (
	eocMarker  = 0xFFF
	badMarker  = 0xFF7
	minCluster = 2
)

var (
	// ErrInvalidArgument is returned for n<1, k<1 or a bad-cluster cluster
	// number outside 2..C+1. It always takes precedence over other causes.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrFileNotFound means the handle is not the head of a live file.
	ErrFileNotFound = errors.New("file not found")
	// ErrNoSpace means fewer than n free clusters are available.
	ErrNoSpace = errors.New("not enough free clusters")
	// ErrOutOfRange means Truncate's k exceeds the chain length.
	ErrOutOfRange = errors.New("truncate length exceeds chain length")
	// ErrClusterNotFree means MarkBad targeted an occupied or bad cluster.
	ErrClusterNotFree = errors.New("cluster is not free")
)

// FAT12 is a concurrent-safe FAT12 cluster-chain manager.
// The zero value is not usable; use New.
type FAT12 struct {
	mu    sync.RWMutex
	c     int
	fat   []uint16
	files map[uint16]struct{}
	rover uint16
}

// New creates a FAT12 image managing c data clusters numbered 2..c+1.
func New(c int) (*FAT12, error) {
	if c < 1 || c > 4078 {
		return nil, ErrInvalidArgument
	}
	f := &FAT12{
		c:     c,
		fat:   make([]uint16, c+2),
		files: make(map[uint16]struct{}),
		rover: minCluster,
	}
	f.fat[0] = 0xFF8
	f.fat[1] = eocMarker
	return f, nil
}

// encodeImage packs the 12-bit entries into the FAT byte image.
func encodeImage(fat []uint16) []byte {
	b := make([]byte, (len(fat)*3+1)/2)
	i := 0
	for ; i+1 < len(fat); i += 2 {
		lo := fat[i]
		hi := fat[i+1]
		o := i + i/2
		b[o] = byte(lo)
		b[o+1] = byte(lo>>8) | byte(hi<<4)
		b[o+2] = byte(hi >> 4)
	}
	if i < len(fat) {
		lo := fat[i]
		o := i + i/2
		b[o] = byte(lo)
		b[o+1] = byte(lo >> 8)
	}
	return b
}

func (f *FAT12) maxCluster() uint16 {
	return uint16(f.c + 1)
}

// freeCount must be called with f.mu held.
func (f *FAT12) freeCount() int {
	n := 0
	for c := minCluster; c <= f.c+1; c++ {
		if f.fat[c] == 0 {
			n++
		}
	}
	return n
}

// chainOf returns the chain starting at h; the bool reports structural validity.
// It must be called with f.mu held.
func (f *FAT12) chainOf(h uint16) ([]uint16, bool) {
	chain := []uint16{h}
	cur := h
	for {
		v := f.fat[cur]
		if v >= 0xFF8 {
			return chain, true
		}
		if v == badMarker || v < minCluster || int(v) > f.c+1 {
			return chain, false
		}
		cur = v
		chain = append(chain, cur)
	}
}

// allocate picks n free clusters: the lowest run of n consecutive free
// clusters starting at rover without wrapping, falling back to a cyclic
// next-fit scan. It must be called with f.mu held after the space check.
func (f *FAT12) allocate(n int) []uint16 {
	runStart := -1
	runLen := 0
	for c := int(f.rover); c <= f.c+1; c++ {
		if f.fat[c] == 0 {
			if runLen == 0 {
				runStart = c
			}
			runLen++
			if runLen == n {
				break
			}
		} else {
			runStart = -1
			runLen = 0
		}
	}

	if runLen == n {
		got := make([]uint16, n)
		for i := range got {
			got[i] = uint16(runStart + i)
		}
		return got
	}

	got := make([]uint16, 0, n)
	for i := 0; i < f.c; i++ {
		c := uint16(minCluster + (int(f.rover)-minCluster+i)%f.c)
		if f.fat[c] == 0 {
			got = append(got, c)
			if len(got) == n {
				break
			}
		}
	}
	return got
}

// writeChain links the given clusters in acquisition order ending in 0xFFF.
// It must be called with f.mu held.
func (f *FAT12) writeChain(chain []uint16) {
	for i := 0; i+1 < len(chain); i++ {
		f.fat[chain[i]] = chain[i+1]
	}
	f.fat[chain[len(chain)-1]] = eocMarker
}

// advanceRover places rover immediately after the last taken cluster.
func (f *FAT12) advanceRover(last uint16) {
	nxt := int(last) + 1
	if nxt > f.c+1 {
		nxt = minCluster
	}
	f.rover = uint16(nxt)
}

func (f *FAT12) rewindRoverTo(clusters []uint16) {
	min := uint16(f.c + 2)
	for _, c := range clusters {
		if c < min {
			min = c
		}
	}
	if min <= f.maxCluster() && min < f.rover {
		f.rover = min
	}
}

// Create allocates a new file of n clusters and returns its handle.
func (f *FAT12) Create(n int) (uint16, error) {
	if n < 1 {
		return 0, ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.freeCount() < n {
		return 0, ErrNoSpace
	}
	got := f.allocate(n)
	f.writeChain(got)
	f.files[got[0]] = struct{}{}
	f.advanceRover(got[len(got)-1])
	return got[0], nil
}

// Extend appends n clusters to the chain of file h.
func (f *FAT12) Extend(h uint16, n int) error {
	if n < 1 {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[h]; !ok {
		return ErrFileNotFound
	}
	if f.freeCount() < n {
		return ErrNoSpace
	}
	chain, ok := f.chainOf(h)
	if !ok {
		return ErrFileNotFound
	}
	got := f.allocate(n)
	f.fat[chain[len(chain)-1]] = got[0]
	f.writeChain(got)
	f.advanceRover(got[len(got)-1])
	return nil
}

// Truncate keeps the first k clusters of file h and frees the rest.
func (f *FAT12) Truncate(h uint16, k int) error {
	if k < 1 {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[h]; !ok {
		return ErrFileNotFound
	}
	chain, ok := f.chainOf(h)
	if !ok {
		return ErrFileNotFound
	}
	if k > len(chain) {
		return ErrOutOfRange
	}
	if k == len(chain) {
		return nil
	}
	released := chain[k:]
	for _, c := range released {
		f.fat[c] = 0
	}
	f.fat[chain[k-1]] = eocMarker
	f.rewindRoverTo(released)
	return nil
}

// Delete frees the whole chain of file h and removes the handle.
func (f *FAT12) Delete(h uint16) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[h]; !ok {
		return ErrFileNotFound
	}
	chain, ok := f.chainOf(h)
	if !ok {
		return ErrFileNotFound
	}
	for _, c := range chain {
		f.fat[c] = 0
	}
	delete(f.files, h)
	f.rewindRoverTo(chain)
	return nil
}

// MarkBad marks a free cluster as bad.
func (f *FAT12) MarkBad(c uint16) error {
	if c < minCluster || int(c) > f.c+1 {
		return ErrInvalidArgument
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fat[c] != 0 {
		return ErrClusterNotFree
	}
	f.fat[c] = badMarker
	return nil
}

// Defrag moves file h onto the lowest clusters in the union of free clusters
// and the file's own clusters, preserving free-cluster count.
func (f *FAT12) Defrag(h uint16) (uint16, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.files[h]; !ok {
		return 0, ErrFileNotFound
	}
	old, ok := f.chainOf(h)
	if !ok {
		return 0, ErrFileNotFound
	}

	oldSet := make(map[uint16]struct{}, len(old))
	for _, c := range old {
		oldSet[c] = struct{}{}
	}

	newChain := make([]uint16, 0, len(old))
	for c := minCluster; c <= f.c+1 && len(newChain) < len(old); c++ {
		cc := uint16(c)
		if f.fat[c] == 0 {
			newChain = append(newChain, cc)
		} else if _, isOld := oldSet[cc]; isOld {
			newChain = append(newChain, cc)
		}
	}

	same := len(newChain) == len(old)
	if same {
		for i := range old {
			if old[i] != newChain[i] {
				same = false
				break
			}
		}
	}
	if same {
		return h, nil
	}

	var released []uint16
	for _, c := range old {
		keep := false
		for _, nc := range newChain {
			if nc == c {
				keep = true
				break
			}
		}
		if !keep {
			released = append(released, c)
		}
	}
	for _, c := range released {
		f.fat[c] = 0
	}
	f.writeChain(newChain)
	f.rewindRoverTo(released)
	delete(f.files, h)
	f.files[newChain[0]] = struct{}{}
	return newChain[0], nil
}

// Image returns a copy of the packed 12-bit FAT byte image.
func (f *FAT12) Image() []byte {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return encodeImage(f.fat)
}

// Chain returns the cluster sequence of file h.
func (f *FAT12) Chain(h uint16) ([]uint16, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if _, ok := f.files[h]; !ok {
		return nil, ErrFileNotFound
	}
	chain, ok := f.chainOf(h)
	if !ok {
		return nil, ErrFileNotFound
	}
	out := make([]uint16, len(chain))
	copy(out, chain)
	return out, nil
}

// Free returns the number of free data clusters (bad clusters excluded).
func (f *FAT12) Free() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.freeCount()
}

// Rover returns the allocation cursor.
func (f *FAT12) Rover() uint16 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.rover
}
