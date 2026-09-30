package fsck

import (
	"fmt"
	"sort"
)

// RepairLog records every action taken during a repair pass, in order.
type RepairLog struct {
	Actions  []string
	DataLoss bool
}

func (l *RepairLog) add(format string, args ...interface{}) {
	l.Actions = append(l.Actions, fmt.Sprintf(format, args...))
}

// RepairOptions controls repair behavior; the zero value is a normal run.
type RepairOptions struct {
	// FailAt injects a failure at the named point ("mid-write" or
	// "before-rename") to verify atomicity. Empty disables injection.
	FailAt string
}

// Repair fixes every inconsistency of img in place following the canonical
// repair order and returns the action log.
func Repair(img *Image) (*RepairLog, error) {
	if !img.Allocated(RootInode) || img.Inodes[RootInode].Type != InodeDir {
		return nil, ErrRootNotDir
	}
	log := &RepairLog{}
	fixSharedBlocks(img, log)
	removeDanglingEntries(img, log)
	fixMultiParentDirs(img, log)
	if err := attachUnreachable(img, log); err != nil {
		return nil, err
	}
	fixLinkCounts(img, log)
	rebuildBitmap(img, log)
	return log, nil
}

// fixSharedBlocks gives every multiply-referenced block to the
// lowest-numbered inode; every other inode receives a copy in the
// lowest-numbered free block, or loses the block (data loss) when no free
// block remains. Out-of-range block pointers are dropped as data loss.
func fixSharedBlocks(img *Image, log *RepairLog) {
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree {
			continue
		}
		in := &img.Inodes[i]
		seen := make(map[uint32]bool)
		for j, b := range in.Blocks {
			if b == 0 {
				continue
			}
			if !img.InRange(b) {
				in.Blocks[j] = 0
				log.DataLoss = true
				log.add("inode %d: dropped out-of-range block pointer %d", i, b)
				continue
			}
			if seen[b] {
				in.Blocks[j] = 0
				log.add("inode %d: dropped duplicate block pointer %d", i, b)
				continue
			}
			seen[b] = true
		}
		compactBlocks(in)
	}

	refs := img.blockRefs()
	blocks := make([]uint32, 0, len(refs))
	for b := range refs {
		blocks = append(blocks, b)
	}
	sort.Slice(blocks, func(i, j int) bool { return blocks[i] < blocks[j] })

	used := img.usedBlockSet()
	for _, b := range blocks {
		owners := refs[b]
		if len(owners) < 2 {
			continue
		}
		for _, other := range owners[1:] {
			free, ok := img.lowestFreeBlock(used)
			if !ok {
				in := &img.Inodes[other]
				removeBlockRef(in, b)
				compactBlocks(in)
				if max := uint32(countBlocks(in)) * img.SB.BlockSize; in.Size > max {
					in.Size = max
				}
				log.DataLoss = true
				log.add("inode %d: no free block to copy shared block %d, truncated from block list (data loss)", other, b)
				continue
			}
			copy(img.BlockData(free), img.BlockData(b))
			used[free] = true
			in := &img.Inodes[other]
			for j, x := range in.Blocks {
				if x == b {
					in.Blocks[j] = free
					break
				}
			}
			log.add("inode %d: shared block %d kept by inode %d; copied to free block %d", other, b, owners[0], free)
		}
	}
}

func compactBlocks(in *Inode) {
	n := 0
	for _, b := range in.Blocks {
		if b != 0 {
			in.Blocks[n] = b
			n++
		}
	}
	for i := n; i < len(in.Blocks); i++ {
		in.Blocks[i] = 0
	}
}

func removeBlockRef(in *Inode, b uint32) {
	for i, x := range in.Blocks {
		if x == b {
			in.Blocks[i] = 0
			return
		}
	}
}

func countBlocks(in *Inode) int {
	n := 0
	for _, b := range in.Blocks {
		if b != 0 {
			n++
		}
	}
	return n
}

// removeDanglingEntries deletes directory entries that point to
// unallocated inodes.
func removeDanglingEntries(img *Image, log *RepairLog) {
	for i := range img.Inodes {
		if img.Inodes[i].Type != InodeDir {
			continue
		}
		removed := img.removeEntriesIf(uint32(i), func(e DirEntry) bool {
			return !img.validTarget(e)
		})
		for _, e := range removed {
			log.add("dir %d: removed dangling entry %q -> inode %d", i, e.Name, e.Inode)
		}
	}
}

// fixMultiParentDirs keeps, for every directory referenced by several
// parents, only the entry in the lowest-numbered parent directory.
func fixMultiParentDirs(img *Image, log *RepairLog) {
	parents := img.dirParents()
	children := make([]uint32, 0, len(parents))
	for child := range parents {
		children = append(children, child)
	}
	sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
	for _, child := range children {
		plist := parents[child]
		if len(plist) < 2 {
			continue
		}
		for _, p := range plist[1:] {
			removed := img.removeEntriesIf(p, func(e DirEntry) bool {
				return e.Inode == child
			})
			for _, e := range removed {
				log.add("dir %d: removed entry %q -> dir %d (kept in lowest parent %d)", p, e.Name, child, plist[0])
			}
		}
	}
}

// attachUnreachable attaches unreachable allocated inodes to the recovery
// directory: only the top of each unreachable directory subtree is
// attached, and for directory cycles the lowest-numbered member. Inodes
// with content are attached under their inode number as name; inodes
// without content are freed.
func attachUnreachable(img *Image, log *RepairLog) error {
	reach := img.reachable()
	parents := img.entryParents()

	inU := func(i uint32) bool {
		return img.Allocated(i) && !reach[i]
	}
	covered := make(map[uint32]bool)
	markSubtree := func(root uint32) {
		queue := []uint32{root}
		covered[root] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if img.Inodes[cur].Type != InodeDir {
				continue
			}
			for _, e := range img.Entries(cur) {
				if img.validTarget(e) && inU(e.Inode) && !covered[e.Inode] {
					covered[e.Inode] = true
					queue = append(queue, e.Inode)
				}
			}
		}
	}

	var unattached []uint32

	// Tops of unreachable directory subtrees: no parent at all (any parent
	// of an unreachable node is itself unreachable). Attach those with
	// content; cover every top so the cycle scan below never walks into
	// them.
	for i := range img.Inodes {
		u := uint32(i)
		if !inU(u) || img.Inodes[i].Type != InodeDir || len(parents[u]) > 0 {
			continue
		}
		if img.hasContent(u) {
			unattached = append(unattached, u)
		}
		markSubtree(u)
	}

	// Directory cycles: every remaining unreachable dir has an unreachable
	// parent, so following parent links must loop. Attach the
	// lowest-numbered member of each cycle.
	for {
		found := false
		for i := range img.Inodes {
			u := uint32(i)
			if !inU(u) || covered[u] || img.Inodes[i].Type != InodeDir {
				continue
			}
			seen := map[uint32]bool{}
			chain := []uint32{}
			cur := u
			for !seen[cur] {
				seen[cur] = true
				chain = append(chain, cur)
				cur = parents[cur][0]
			}
			start := 0
			for idx, x := range chain {
				if x == cur {
					start = idx
					break
				}
			}
			loop := chain[start:]
			min := loop[0]
			for _, x := range loop[1:] {
				if x < min {
					min = x
				}
			}
			unattached = append(unattached, min)
			markSubtree(min)
			log.add("dir cycle detected; attaching lowest member %d", min)
			found = true
			break
		}
		if !found {
			break
		}
	}

	// Remaining unreachable non-directories with content that no attached
	// directory carries.
	for i := range img.Inodes {
		u := uint32(i)
		if !inU(u) || covered[u] || img.Inodes[i].Type == InodeDir {
			continue
		}
		if img.hasContent(u) {
			unattached = append(unattached, u)
		}
	}

	return finishAttach(img, log, unattached)
}

// finishAttach attaches the selected inodes to the recovery directory and
// frees every still-unreachable inode without content.
func finishAttach(img *Image, log *RepairLog, unattached []uint32) error {
	if len(unattached) > 0 {
		rec, err := ensureRecoveryDir(img, log)
		if err != nil {
			return err
		}
		sort.Slice(unattached, func(i, j int) bool { return unattached[i] < unattached[j] })
		for _, u := range unattached {
			name := itoa(u)
			if _, exists := img.findEntry(rec, name); exists {
				continue
			}
			if err := img.addEntry(rec, DirEntry{Inode: u, Name: name}); err != nil {
				return err
			}
			log.add("attached unreachable inode %d to recovery dir %d as %q", u, rec, name)
			if img.Inodes[u].Type == InodeDir {
				// Break every other inbound link so the attached
				// directory keeps exactly one parent. Removing edges
				// into u never disconnects anything reachable from u.
				for d := range img.Inodes {
					if uint32(d) == rec || img.Inodes[d].Type != InodeDir {
						continue
					}
					removed := img.removeEntriesIf(uint32(d), func(e DirEntry) bool {
						return e.Inode == u
					})
					for _, e := range removed {
						log.add("dir %d: removed entry %q -> dir %d (single parent kept at recovery dir %d)", d, e.Name, u, rec)
					}
				}
			}
		}
	}

	reach := img.reachable()
	for i := range img.Inodes {
		u := uint32(i)
		if img.Inodes[i].Type == InodeFree || reach[u] {
			continue
		}
		if img.hasContent(u) {
			// Content-bearing inodes that are still unreachable should
			// have been attached; treat as a safety net and attach now.
			rec, err := ensureRecoveryDir(img, log)
			if err != nil {
				return err
			}
			name := itoa(u)
			if _, exists := img.findEntry(rec, name); !exists {
				if err := img.addEntry(rec, DirEntry{Inode: u, Name: name}); err != nil {
					return err
				}
				log.add("attached unreachable inode %d to recovery dir %d as %q", u, rec, name)
			}
			continue
		}
		img.Inodes[i] = Inode{}
		log.add("freed unreachable inode %d without content", u)
	}
	return nil
}

// ensureRecoveryDir returns the inode number of the recovery directory,
// creating it under the root when missing.
func ensureRecoveryDir(img *Image, log *RepairLog) (uint32, error) {
	if e, ok := img.findEntry(RootInode, RecoveryDirName); ok {
		if !img.Allocated(e.Inode) || img.Inodes[e.Inode].Type != InodeDir {
			return 0, ErrRecoveryNameConflict
		}
		return e.Inode, nil
	}
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree {
			img.Inodes[i] = Inode{Type: InodeDir}
			if err := img.addEntry(RootInode, DirEntry{Inode: uint32(i), Name: RecoveryDirName}); err != nil {
				img.Inodes[i] = Inode{}
				return 0, err
			}
			log.add("created recovery dir %q as inode %d", RecoveryDirName, i)
			return uint32(i), nil
		}
	}
	return 0, ErrNoFreeInode
}

// fixLinkCounts sets every allocated inode's link count to its actual
// number of directory entries. The root directory counts itself once.
func fixLinkCounts(img *Image, log *RepairLog) {
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
			log.add("inode %d: nlink %d -> %d", i, img.Inodes[i].Nlink, expected)
			img.Inodes[i].Nlink = uint32(expected)
		}
	}
}

// rebuildBitmap rewrites the bitmap from actual block references.
func rebuildBitmap(img *Image, log *RepairLog) {
	exp := img.expectedBitmap()
	changed := 0
	for b := uint32(0); b < img.SB.TotalBlocks; b++ {
		if img.Bitmap[b] != exp[b] {
			img.Bitmap[b] = exp[b]
			changed++
		}
	}
	if changed > 0 {
		log.add("rebuilt bitmap: %d bits corrected", changed)
	}
}

func itoa(u uint32) string {
	if u == 0 {
		return "0"
	}
	var buf [10]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}
