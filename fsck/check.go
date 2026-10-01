package fsck

import (
	"fmt"
	"sort"
	"strings"
)

// Category identifies a class of inconsistency. The declaration order is
// also the canonical sort order of findings.
type Category int

const (
	CatSharedBlock Category = iota
	CatDanglingEntry
	CatMultiParent
	CatUnreachable
	CatLinkCount
	CatBitmap
)

func (c Category) String() string {
	switch c {
	case CatSharedBlock:
		return "shared-block"
	case CatDanglingEntry:
		return "dangling-dir-entry"
	case CatMultiParent:
		return "multi-parent-dir"
	case CatUnreachable:
		return "unreachable-inode"
	case CatLinkCount:
		return "link-count-mismatch"
	case CatBitmap:
		return "bitmap-mismatch"
	}
	return "unknown"
}

// Finding is a single detected inconsistency. Unused fields are -1 / "".
type Finding struct {
	Category Category
	Inode    int // primary inode the finding is about
	Block    int
	Parent   int
	Name     string
	Expected int
	Actual   int
}

func (f Finding) String() string {
	switch f.Category {
	case CatSharedBlock:
		return fmt.Sprintf("shared-block inode=%d block=%d refs=%d", f.Inode, f.Block, f.Actual)
	case CatDanglingEntry:
		return fmt.Sprintf("dangling-dir-entry parent=%d name=%q target=%d", f.Parent, f.Name, f.Actual)
	case CatMultiParent:
		return fmt.Sprintf("multi-parent-dir inode=%d extra-parent=%d parents=%d", f.Inode, f.Parent, f.Actual)
	case CatUnreachable:
		return fmt.Sprintf("unreachable-inode inode=%d", f.Inode)
	case CatLinkCount:
		return fmt.Sprintf("link-count-mismatch inode=%d expected=%d actual=%d", f.Inode, f.Expected, f.Actual)
	case CatBitmap:
		return fmt.Sprintf("bitmap-mismatch block=%d expected=%d actual=%d", f.Block, f.Expected, f.Actual)
	}
	return "unknown"
}

// Report is the deterministic result of a read-only check.
type Report struct {
	Findings []Finding
}

// Text renders the findings as a stable, byte-identical text form.
func (r *Report) Text() string {
	var b strings.Builder
	if len(r.Findings) == 0 {
		b.WriteString("clean: no findings\n")
		return b.String()
	}
	for _, f := range r.Findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

func sortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Inode != b.Inode {
			return a.Inode < b.Inode
		}
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		if a.Parent != b.Parent {
			return a.Parent < b.Parent
		}
		return a.Name < b.Name
	})
}

// blockRefs maps each referenced data block to the sorted list of allocated
// inode numbers referencing it.
func (img *Image) blockRefs() map[int][]int {
	refs := make(map[int][]int)
	for i, in := range img.Inodes {
		if in.Type == TypeFree {
			continue
		}
		for _, b := range in.Blocks {
			if int(b) < img.BlockCount {
				refs[int(b)] = append(refs[int(b)], i)
			}
		}
	}
	for b := range refs {
		sort.Ints(refs[b])
	}
	return refs
}

// reachable returns the set of allocated inodes reachable from the root by
// following directory entries. Cycle-safe.
func (img *Image) reachable() map[int]bool {
	reach := make(map[int]bool)
	if img.RootInode < 0 || img.RootInode >= img.InodeCount {
		return reach
	}
	if img.Inodes[img.RootInode].Type != TypeDir {
		return reach
	}
	stack := []int{img.RootInode}
	reach[img.RootInode] = true
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if img.Inodes[n].Type != TypeDir {
			continue
		}
		for _, e := range img.readEntries(n) {
			if e.Inode < 0 || e.Inode >= img.InodeCount {
				continue
			}
			if img.Inodes[e.Inode].Type == TypeFree {
				continue
			}
			if !reach[e.Inode] {
				reach[e.Inode] = true
				stack = append(stack, e.Inode)
			}
		}
	}
	return reach
}

// entryCounts returns, for every inode, how many directory entries across
// all allocated directories point at it.
func (img *Image) entryCounts() map[int]int {
	counts := make(map[int]int)
	for i, in := range img.Inodes {
		if in.Type != TypeDir {
			continue
		}
		for _, e := range img.readEntries(i) {
			counts[e.Inode]++
		}
	}
	return counts
}

// check scans the image and returns all inconsistencies, sorted by category
// and inode number. It never mutates the image.
func check(img *Image) []Finding {
	var fs []Finding
	refs := img.blockRefs()

	// 1. Blocks referenced by more than one inode. The lowest-numbered
	// inode is the owner; every other reference is a finding.
	for b, owners := range refs {
		if len(owners) < 2 {
			continue
		}
		seen := make(map[int]bool)
		for _, n := range owners[1:] {
			if n == owners[0] || seen[n] {
				continue
			}
			seen[n] = true
			fs = append(fs, Finding{Category: CatSharedBlock, Inode: n, Block: b, Parent: -1, Actual: len(owners)})
		}
	}

	// 2. Directory entries pointing at unallocated or out-of-range inodes.
	for i, in := range img.Inodes {
		if in.Type != TypeDir {
			continue
		}
		for _, e := range img.readEntries(i) {
			if e.Inode < 0 || e.Inode >= img.InodeCount || img.Inodes[e.Inode].Type == TypeFree {
				fs = append(fs, Finding{Category: CatDanglingEntry, Inode: i, Block: -1, Parent: i, Name: e.Name, Actual: e.Inode})
			}
		}
	}

	// 3. Subdirectories collected by more than one parent directory.
	parents := make(map[int][]int)
	for i, in := range img.Inodes {
		if in.Type != TypeDir {
			continue
		}
		for _, e := range img.readEntries(i) {
			if e.Inode < 0 || e.Inode >= img.InodeCount {
				continue
			}
			if img.Inodes[e.Inode].Type == TypeDir {
				parents[e.Inode] = append(parents[e.Inode], i)
			}
		}
	}
	for child, ps := range parents {
		if len(ps) < 2 {
			continue
		}
		sort.Ints(ps)
		for _, p := range ps[1:] {
			fs = append(fs, Finding{Category: CatMultiParent, Inode: child, Block: -1, Parent: p, Actual: len(ps)})
		}
	}

	// 4. Allocated inodes unreachable from the root.
	reach := img.reachable()
	for i, in := range img.Inodes {
		if in.Type != TypeFree && !reach[i] {
			fs = append(fs, Finding{Category: CatUnreachable, Inode: i, Block: -1, Parent: -1})
		}
	}

	// 5. Link counts that differ from the actual number of entries.
	counts := img.entryCounts()
	for i, in := range img.Inodes {
		if in.Type == TypeFree {
			continue
		}
		if int(in.Nlink) != counts[i] {
			fs = append(fs, Finding{Category: CatLinkCount, Inode: i, Block: -1, Parent: -1, Expected: counts[i], Actual: int(in.Nlink)})
		}
	}

	// 6. Bitmap bits that disagree with actual block references.
	for b := 0; b < img.BlockCount; b++ {
		expected := len(refs[b]) > 0
		if img.Bitmap[b] != expected {
			fs = append(fs, Finding{Category: CatBitmap, Inode: -1, Block: b, Parent: -1, Expected: boolInt(expected), Actual: boolInt(img.Bitmap[b])})
		}
	}

	sortFindings(fs)
	return fs
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
