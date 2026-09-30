package fsck

import (
	"fmt"
	"sort"
	"strings"
)

// Category identifies the class of an inconsistency. The declaration order
// is the canonical sort order of findings.
type Category int

const (
	CatSharedBlock Category = iota
	CatDanglingDirent
	CatMultiParentDir
	CatUnreachableInode
	CatBadLinkCount
	CatBitmapMismatch
)

func (c Category) String() string {
	switch c {
	case CatSharedBlock:
		return "shared-block"
	case CatDanglingDirent:
		return "dangling-dirent"
	case CatMultiParentDir:
		return "multi-parent-dir"
	case CatUnreachableInode:
		return "unreachable-inode"
	case CatBadLinkCount:
		return "bad-link-count"
	case CatBitmapMismatch:
		return "bitmap-mismatch"
	}
	return "unknown"
}

// Finding describes one detected inconsistency.
type Finding struct {
	Category Category
	Inode    uint32
	Block    uint32
	Name     string
	Detail   string
}

func (f Finding) String() string {
	return fmt.Sprintf("[%s] inode=%d block=%d name=%q %s",
		f.Category, f.Inode, f.Block, f.Name, f.Detail)
}

// Report is the deterministic result of one check pass.
type Report struct {
	Findings []Finding
}

// Clean reports whether the report contains no findings.
func (r *Report) Clean() bool {
	return len(r.Findings) == 0
}

// String renders the report as a deterministic, line-oriented text.
func (r *Report) String() string {
	if r.Clean() {
		return "clean: no findings"
	}
	lines := make([]string, len(r.Findings))
	for i, f := range r.Findings {
		lines[i] = f.String()
	}
	return strings.Join(lines, "\n")
}

// validTarget reports whether e points at an allocated inode.
func (img *Image) validTarget(e DirEntry) bool {
	return e.Inode != 0 && img.Allocated(e.Inode)
}

// blockRefs maps each in-range referenced data block to the ascending list
// of inode numbers referencing it.
func (img *Image) blockRefs() map[uint32][]uint32 {
	refs := make(map[uint32][]uint32)
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree {
			continue
		}
		seen := make(map[uint32]bool)
		for _, b := range img.Inodes[i].Blocks {
			if b != 0 && img.InRange(b) && !seen[b] {
				seen[b] = true
				refs[b] = append(refs[b], uint32(i))
			}
		}
	}
	return refs
}

// linkCounts counts valid directory entries pointing at each inode.
func (img *Image) linkCounts() map[uint32]int {
	counts := make(map[uint32]int)
	for i := range img.Inodes {
		if img.Inodes[i].Type != InodeDir {
			continue
		}
		for _, e := range img.Entries(uint32(i)) {
			if img.validTarget(e) {
				counts[e.Inode]++
			}
		}
	}
	return counts
}

// dirParents maps each allocated directory to the sorted list of distinct
// parent directory inode numbers that contain a valid entry for it.
func (img *Image) dirParents() map[uint32][]uint32 {
	return img.parentsOf(func(target uint32) bool {
		return img.Inodes[target].Type == InodeDir
	})
}

// entryParents maps every inode referenced by at least one valid directory
// entry to the sorted list of distinct parent directory inode numbers.
func (img *Image) entryParents() map[uint32][]uint32 {
	return img.parentsOf(func(uint32) bool { return true })
}

func (img *Image) parentsOf(accept func(target uint32) bool) map[uint32][]uint32 {
	parents := make(map[uint32][]uint32)
	for i := range img.Inodes {
		if img.Inodes[i].Type != InodeDir {
			continue
		}
		for _, e := range img.Entries(uint32(i)) {
			if !img.validTarget(e) || !accept(e.Inode) {
				continue
			}
			lst := parents[e.Inode]
			found := false
			for _, p := range lst {
				if p == uint32(i) {
					found = true
					break
				}
			}
			if !found {
				parents[e.Inode] = append(lst, uint32(i))
			}
		}
	}
	for k := range parents {
		sort.Slice(parents[k], func(a, b int) bool { return parents[k][a] < parents[k][b] })
	}
	return parents
}

// reachable returns the set of allocated inodes reachable from the root
// directory by following valid directory entries.
func (img *Image) reachable() map[uint32]bool {
	reach := make(map[uint32]bool)
	if !img.Allocated(RootInode) || img.Inodes[RootInode].Type != InodeDir {
		return reach
	}
	queue := []uint32{RootInode}
	reach[RootInode] = true
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if img.Inodes[cur].Type != InodeDir {
			continue
		}
		for _, e := range img.Entries(cur) {
			if !img.validTarget(e) || reach[e.Inode] {
				continue
			}
			reach[e.Inode] = true
			queue = append(queue, e.Inode)
		}
	}
	return reach
}

// expectedBitmap computes the bitmap implied by actual block references:
// metadata blocks are always used; a data block is used exactly when some
// allocated inode references it.
func (img *Image) expectedBitmap() []bool {
	exp := make([]bool, img.SB.TotalBlocks)
	for b := uint32(0); b < img.SB.DataStart; b++ {
		exp[b] = true
	}
	for b := range img.blockRefs() {
		exp[b] = true
	}
	return exp
}

// Check scans the image and returns all inconsistencies sorted by category
// and inode number. It never modifies the image.
func Check(img *Image) (*Report, error) {
	if !img.Allocated(RootInode) || img.Inodes[RootInode].Type != InodeDir {
		return nil, ErrRootNotDir
	}
	var findings []Finding

	refs := img.blockRefs()
	blocks := make([]uint32, 0, len(refs))
	for b := range refs {
		blocks = append(blocks, b)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i] < blocks[j] })
	for _, b := range blocks {
		if len(refs[b]) > 1 {
			findings = append(findings, Finding{
				Category: CatSharedBlock,
				Inode:    refs[b][0],
				Block:    b,
				Detail:   fmt.Sprintf("block %d referenced by inodes %v", b, refs[b]),
			})
		}
	}

	for i := range img.Inodes {
		if img.Inodes[i].Type != InodeDir {
			continue
		}
		for _, e := range img.Entries(uint32(i)) {
			if img.validTarget(e) {
				continue
			}
			findings = append(findings, Finding{
				Category: CatDanglingDirent,
				Inode:    uint32(i),
				Name:     e.Name,
				Detail:   fmt.Sprintf("entry %q points to unallocated inode %d", e.Name, e.Inode),
			})
		}
	}

	parents := img.dirParents()
	for child, plist := range parents {
		if len(plist) > 1 {
			findings = append(findings, Finding{
				Category: CatMultiParentDir,
				Inode:    child,
				Detail:   fmt.Sprintf("directory referenced by parents %v", plist),
			})
		}
	}

	reach := img.reachable()
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree || reach[uint32(i)] {
			continue
		}
		findings = append(findings, Finding{
			Category: CatUnreachableInode,
			Inode:    uint32(i),
			Detail: fmt.Sprintf("allocated type=%d has-content=%t",
				img.Inodes[i].Type, img.hasContent(uint32(i))),
		})
	}

	counts := img.linkCounts()
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree {
			continue
		}
		expected := counts[uint32(i)]
		if i == RootInode && expected == 0 {
			expected = 1
		}
		if int(img.Inodes[i].Nlink) != expected {
			findings = append(findings, Finding{
				Category: CatBadLinkCount,
				Inode:    uint32(i),
				Detail: fmt.Sprintf("nlink=%d but actually referenced %d times",
					img.Inodes[i].Nlink, expected),
			})
		}
	}

	exp := img.expectedBitmap()
	for b := uint32(0); b < img.SB.TotalBlocks; b++ {
		if img.Bitmap[b] != exp[b] {
			findings = append(findings, Finding{
				Category: CatBitmapMismatch,
				Block:    b,
				Detail: fmt.Sprintf("bitmap says used=%t but actual reference says used=%t",
					img.Bitmap[b], exp[b]),
			})
		}
	}

	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Category != b.Category {
			return a.Category < b.Category
		}
		if a.Inode != b.Inode {
			return a.Inode < b.Inode
		}
		if a.Block != b.Block {
			return a.Block < b.Block
		}
		return a.Name < b.Name
	})
	return &Report{Findings: findings}, nil
}
