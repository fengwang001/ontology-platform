// Package hashpart assigns keys to a fixed number of partitions and
// defines the on-disk spill segment format.
package hashpart

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Partition maps key to one of n partitions (n > 0). Stable across runs.
func Partition(key string, n int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return int(h.Sum32() % uint32(n))
}

// SegmentBase returns the base name (no extension) of a spill segment.
func SegmentBase(part, seq int) string {
	return fmt.Sprintf("part-%03d-%06d", part, seq)
}

// ListSegments returns the sorted .spill file paths of one partition.
func ListSegments(dir string, part int) ([]string, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	prefix := fmt.Sprintf("part-%03d-", part)
	var out []string
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".spill") {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// CleanTemps removes leftover *.tmp files (crashed atomic writes) from dir.
// It returns the number of files removed.
func CleanTemps(dir string) (int, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

// CountTemps reports how many *.tmp files currently exist in dir.
func CountTemps(dir string) int {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return -1
	}
	n := 0
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".tmp") {
			n++
		}
	}
	return n
}
