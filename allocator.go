package smartlocker

import (
	"math/bits"
	"sort"
	"strings"
)

const bitmapLevels = 11

type bitmapSet struct {
	levels [bitmapLevels]map[uint64]uint64
}

func newBitmapSet() *bitmapSet {
	var set bitmapSet
	for level := range set.levels {
		set.levels[level] = make(map[uint64]uint64)
	}
	return &set
}

func (set *bitmapSet) add(rank uint64) {
	for level := 0; level < bitmapLevels; level++ {
		key := rank >> (6 * (level + 1))
		bit := (rank >> (6 * level)) & 63
		set.levels[level][key] |= 1 << bit
	}
}

func (set *bitmapSet) remove(rank uint64) {
	level := 0
	key := rank >> 6
	bit := rank & 63
	set.levels[level][key] &^= 1 << bit
	if set.levels[level][key] == 0 {
		delete(set.levels[level], key)
	}
	for level = 1; level < bitmapLevels; level++ {
		childKey := rank >> (6 * level)
		key := rank >> (6 * (level + 1))
		bit := (rank >> (6 * level)) & 63
		if set.levels[level-1][childKey] == 0 {
			set.levels[level][key] &^= 1 << bit
			if set.levels[level][key] == 0 {
				delete(set.levels[level], key)
			}
		}
	}
}

func (set *bitmapSet) min() (uint64, bool, int) {
	if len(set.levels[bitmapLevels-1]) == 0 {
		return 0, false, 1
	}
	probes := 1
	key := uint64(0)
	for level := bitmapLevels - 1; level >= 0; level-- {
		word := set.levels[level][key]
		bit := bits.TrailingZeros64(word)
		key = key<<6 | uint64(bit)
		probes++
	}
	return key, true, probes
}

func (set *bitmapSet) any() bool {
	return len(set.levels[bitmapLevels-1]) != 0
}

type cellAllocator struct {
	rankByID    map[string]uint64
	idByRank    map[uint64]string
	sizeByID    map[string]Size
	free        [3]*bitmapSet
	totalBySize [3]int
	lastProbes  int64
	totalProbes int64
	maxProbes   int64
}

func newCellAllocator(cells map[string]Size) *cellAllocator {
	ids := make([]string, 0, len(cells))
	for id := range cells {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		return compareCellID(ids[i], ids[j]) < 0
	})

	allocator := &cellAllocator{
		rankByID: make(map[string]uint64, len(ids)),
		idByRank: make(map[uint64]string, len(ids)),
		sizeByID: make(map[string]Size, len(ids)),
	}
	for i := range allocator.free {
		allocator.free[i] = newBitmapSet()
	}
	for rank, id := range ids {
		index := uint64(rank)
		size := cells[id]
		allocator.rankByID[id] = index
		allocator.idByRank[index] = id
		allocator.sizeByID[id] = size
		allocator.totalBySize[size-1]++
		allocator.free[size-1].add(index)
	}
	return allocator
}

func (allocator *cellAllocator) allocate(size Size) (string, bool) {
	rank := uint64(0)
	ok := false
	probes := 0
	for cellSize := size; cellSize <= Large; cellSize++ {
		set := allocator.free[cellSize-1]
		probes++
		if !set.any() {
			continue
		}
		var setProbes int
		rank, ok, setProbes = set.min()
		probes += setProbes
		break
	}
	allocator.lastProbes = int64(probes)
	allocator.totalProbes += int64(probes)
	if int64(probes) > allocator.maxProbes {
		allocator.maxProbes = int64(probes)
	}
	if !ok {
		return "", false
	}
	id := allocator.idByRank[rank]
	allocator.free[allocator.sizeByID[id]-1].remove(rank)
	return id, true
}

func (allocator *cellAllocator) release(id string) {
	rank := allocator.rankByID[id]
	size := allocator.sizeByID[id]
	allocator.free[size-1].add(rank)
}

func (allocator *cellAllocator) hasCompatible(size Size) bool {
	for cellSize := size; cellSize <= Large; cellSize++ {
		if allocator.totalBySize[cellSize-1] > 0 {
			return true
		}
	}
	return false
}

func compareCellID(left, right string) int {
	leftIndex, rightIndex := 0, 0
	for leftIndex < len(left) && rightIndex < len(right) {
		leftDigit := isDigit(left[leftIndex])
		rightDigit := isDigit(right[rightIndex])
		if leftDigit != rightDigit {
			if left[leftIndex] < right[rightIndex] {
				return -1
			}
			return 1
		}
		if leftDigit {
			leftEnd := leftIndex + 1
			for leftEnd < len(left) && isDigit(left[leftEnd]) {
				leftEnd++
			}
			rightEnd := rightIndex + 1
			for rightEnd < len(right) && isDigit(right[rightEnd]) {
				rightEnd++
			}
			leftNumber := strings.TrimLeft(left[leftIndex:leftEnd], "0")
			rightNumber := strings.TrimLeft(right[rightIndex:rightEnd], "0")
			if len(leftNumber) != len(rightNumber) {
				if len(leftNumber) < len(rightNumber) {
					return -1
				}
				return 1
			}
			if leftNumber != rightNumber {
				return strings.Compare(leftNumber, rightNumber)
			}
			leftIndex = leftEnd
			rightIndex = rightEnd
			continue
		}
		if left[leftIndex] != right[rightIndex] {
			if left[leftIndex] < right[rightIndex] {
				return -1
			}
			return 1
		}
		leftIndex++
		rightIndex++
	}
	switch {
	case leftIndex == len(left) && rightIndex == len(right):
		return 0
	case leftIndex == len(left):
		return -1
	default:
		return 1
	}
}

func isDigit(value byte) bool {
	return value >= '0' && value <= '9'
}
