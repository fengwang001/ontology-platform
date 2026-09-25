// Package sfano builds Shannon-Fano prefix code tables by recursive
// splitting. It depends on nothing outside the standard library.
package sfano

import (
	"errors"
	"sort"
)

// ErrInvalidFreq is returned when the frequency table is empty, sums to
// zero, or contains a negative frequency.
var ErrInvalidFreq = errors.New("sfano: invalid frequency table")

type item struct {
	sym  byte
	freq int
}

// Build sorts symbols by (frequency desc, symbol asc) and recursively
// splits groups, returning the code table symbol -> bit string.
// The result is deterministic for a given input.
func Build(freq map[byte]int) (map[byte]string, error) {
	if len(freq) == 0 {
		return nil, ErrInvalidFreq
	}
	items := make([]item, 0, len(freq))
	total := 0
	for s, f := range freq {
		if f < 0 {
			return nil, ErrInvalidFreq
		}
		total += f
		items = append(items, item{sym: s, freq: f})
	}
	if total == 0 {
		return nil, ErrInvalidFreq
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].freq != items[j].freq {
			return items[i].freq > items[j].freq
		}
		return items[i].sym < items[j].sym
	})
	codes := make(map[byte]string, len(items))
	split(items, "", codes)
	return codes, nil
}

// split cuts g at the position minimizing |left-right|; ties resolve to
// the leftmost cut (fewest symbols on the left). Left half appends '0',
// right half appends '1'. Recursion ends at single-symbol groups.
func split(g []item, prefix string, codes map[byte]string) {
	if len(g) == 1 {
		codes[g[0].sym] = prefix
		return
	}
	total := 0
	for _, it := range g {
		total += it.freq
	}
	best, bestDiff, left := 1, total, 0
	for i := 0; i < len(g)-1; i++ {
		left += g[i].freq
		if d := abs(2*left - total); d < bestDiff {
			bestDiff, best = d, i+1
		}
	}
	split(g[:best], prefix+"0", codes)
	split(g[best:], prefix+"1", codes)
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
