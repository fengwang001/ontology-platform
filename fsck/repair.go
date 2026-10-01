package fsck

import (
	"fmt"
	"sort"
	"strconv"
)

// Action describes one repair step applied to the working copy. Actions are
// recorded in the exact order they are applied.
type Action struct {
	Kind     string
	Inode    int
	Block    int
	Parent   int
	Name     string
	DataLoss bool
	Detail   string
}

func (a Action) String() string {
	s := fmt.Sprintf("%s inode=%d", a.Kind, a.Inode)
	if a.Block >= 0 {
		s += fmt.Sprintf(" block=%d", a.Block)
	}
	if a.Parent >= 0 {
		s += fmt.Sprintf(" parent=%d", a.Parent)
	}
	if a.Name != "" {
		s += fmt.Sprintf(" name=%q", a.Name)
	}
	if a.Detail != "" {
		s += " " + a.Detail
	}
	if a.DataLoss {
		s += " [DATA-LOSS]"
	}
	return s
}

// RepairResult summarizes a completed repair pass.
type RepairResult struct {
	Findings []Finding
	Actions  []Action
	DataLoss bool
}

// Text renders findings and actions deterministically.
func (r *RepairResult) Text() string {
	s := "findings:\n"
	if len(r.Findings) == 0 {
		s += "  (none)\n"
	}
	for _, f := range r.Findings {
		s += "  " + f.String() + "\n"
	}
	s += "actions:\n"
	if len(r.Actions) == 0 {
		s += "  (none)\n"
	}
	for _, a := range r.Actions {
		s += "  " + a.String() + "\n"
	}
	return s
}

// repair applies the fixed repair pipeline to img (already a private copy).
// The stage order is part of the specification and must not change:
//
//  1. shared data blocks
//  2. dangling directory entries
//  3. directories collected by multiple parents
//  4. unreachable inodes (attach to recovery dir or free)
//  5. link counts
//  6. block bitmap
func repair(img *Image) (*RepairResult, error) {
	res := &RepairResult{Findings: check(img)}
	log := func(a Action) {
		res.Actions = append(res.Actions, a)
		if a.DataLoss {
			res.DataLoss = true
		}
	}

	// Stage 1: a block referenced by several inodes stays with the
	// lowest-numbered inode; every other inode receives a copy in the
	// lowest-numbered free block, or loses the reference when no free
	// block remains (data loss).
	refs := img.blockRefs()
	blocks := make([]int, 0, len(refs))
	for b := range refs {
		blocks = append(blocks, b)
	}
	sort.Ints(blocks)
	for _, b := range blocks {
		owners := refs[b]
		if len(owners) < 2 {
			continue
		}
		keeper := owners[0]
		for _, n := range owners[1:] {
			if n == keeper {
				continue
			}
			in := &img.Inodes[n]
			for s := 0; s < len(in.Blocks); {
				if int(in.Blocks[s]) != b {
					s++
					continue
				}
				free := img.lowestFreeBlock()
				if free >= 0 {
					copy(img.Blocks[free], img.Blocks[b])
					in.Blocks[s] = uint32(free)
					log(Action{Kind: "shared-block-copy", Inode: n, Block: free, Parent: keeper,
						Detail: fmt.Sprintf("block %d kept by inode %d; content copied to block %d", b, keeper, free)})
					s++
				} else {
					in.Blocks = append(in.Blocks[:s], in.Blocks[s+1:]...)
					log(Action{Kind: "shared-block-truncate", Inode: n, Block: b, Parent: keeper, DataLoss: true,
						Detail: fmt.Sprintf("no free block; reference to shared block %d removed", b)})
				}
			}
		}
	}

	// Stage 2: drop directory entries pointing at unallocated or
	// out-of-range inodes.
	for i := range img.Inodes {
		if img.Inodes[i].Type != TypeDir {
			continue
		}
		entries := img.readEntries(i)
		kept := entries[:0]
		dirty := false
		for _, e := range entries {
			if e.Inode < 0 || e.Inode >= img.InodeCount || img.Inodes[e.Inode].Type == TypeFree {
				log(Action{Kind: "drop-dangling-entry", Inode: i, Block: -1, Parent: i, Name: e.Name,
					Detail: fmt.Sprintf("target inode %d is not allocated", e.Inode)})
				dirty = true
				continue
			}
			kept = append(kept, e)
		}
		if dirty {
			if err := img.writeEntries(i, kept); err != nil {
				return nil, err
			}
		}
	}

	// Stage 3: a subdirectory collected by several parents stays only in
	// the parent with the lowest inode number.
	parents := make(map[int][]int)
	for i := range img.Inodes {
		if img.Inodes[i].Type != TypeDir {
			continue
		}
		for _, e := range img.readEntries(i) {
			if e.Inode >= 0 && e.Inode < img.InodeCount && img.Inodes[e.Inode].Type == TypeDir {
				parents[e.Inode] = append(parents[e.Inode], i)
			}
		}
	}
	children := make([]int, 0, len(parents))
	for c := range parents {
		children = append(children, c)
	}
	sort.Ints(children)
	for _, child := range children {
		ps := parents[child]
		if len(ps) < 2 {
			continue
		}
		sort.Ints(ps)
		keeper := ps[0]
		for _, p := range ps[1:] {
			entries := img.readEntries(p)
			kept := entries[:0]
			for _, e := range entries {
				if e.Inode == child {
					log(Action{Kind: "drop-extra-parent", Inode: child, Block: -1, Parent: p, Name: e.Name,
						Detail: fmt.Sprintf("directory stays with parent inode %d", keeper)})
					continue
				}
				kept = append(kept, e)
			}
			if err := img.writeEntries(p, kept); err != nil {
				return nil, err
			}
		}
	}

	// Stage 4: re-attach or free unreachable inodes.
	if err := repairUnreachable(img, log); err != nil {
		return nil, err
	}

	// Stage 5: link counts become the actual number of collecting entries.
	counts := img.entryCounts()
	for i := range img.Inodes {
		if img.Inodes[i].Type == TypeFree {
			img.Inodes[i].Nlink = 0
			continue
		}
		actual := counts[i]
		if int(img.Inodes[i].Nlink) != actual {
			log(Action{Kind: "fix-link-count", Inode: i, Block: -1, Parent: -1,
				Detail: fmt.Sprintf("nlink %d -> %d", img.Inodes[i].Nlink, actual)})
			img.Inodes[i].Nlink = uint16(actual)
		}
	}

	// Stage 6: rebuild the bitmap from actual block references.
	used := img.usedBlocks()
	for b := 0; b < img.BlockCount; b++ {
		want := used[b]
		if img.Bitmap[b] != want {
			log(Action{Kind: "fix-bitmap", Inode: -1, Block: b, Parent: -1,
				Detail: fmt.Sprintf("bit %d -> %d", boolInt(img.Bitmap[b]), boolInt(want))})
			img.Bitmap[b] = want
		}
	}

	return res, nil
}

// hasContent reports whether an inode carries data worth recovering: a file
// with blocks, or a directory with at least one entry.
func (img *Image) hasContent(n int) bool {
	in := &img.Inodes[n]
	switch in.Type {
	case TypeFile:
		return len(in.Blocks) > 0
	case TypeDir:
		return len(img.readEntries(n)) > 0
	}
	return false
}

// repairUnreachable attaches reachable-from-nothing inodes to the recovery
// directory (top of subtree only, smallest inode of a cycle) and frees
// unreachable inodes without content.
func repairUnreachable(img *Image, log func(Action)) error {
	recDir := -1
	ensureRecDir := func() (int, error) {
		if recDir >= 0 {
			return recDir, nil
		}
		for _, e := range img.readEntries(img.RootInode) {
			if e.Name != RecoveryName {
				continue
			}
			if img.Inodes[e.Inode].Type != TypeDir {
				return -1, ErrRecoveryNameTaken
			}
			recDir = e.Inode
			return recDir, nil
		}
		free := -1
		for i := range img.Inodes {
			if img.Inodes[i].Type == TypeFree {
				free = i
				break
			}
		}
		if free < 0 {
			return -1, ErrNoFreeInode
		}
		img.Inodes[free] = Inode{Type: TypeDir}
		rootEntries := append(img.readEntries(img.RootInode), DirEntry{Inode: free, Name: RecoveryName})
		if err := img.writeEntries(img.RootInode, rootEntries); err != nil {
			return -1, err
		}
		log(Action{Kind: "create-recovery-dir", Inode: free, Block: -1, Parent: img.RootInode, Name: RecoveryName})
		recDir = free
		return recDir, nil
	}

	for {
		reach := img.reachable()
		var unreach []int
		for i := range img.Inodes {
			if img.Inodes[i].Type != TypeFree && !reach[i] {
				unreach = append(unreach, i)
			}
		}
		// Candidates: unreachable inodes with content that are not
		// referenced by any other unreachable inode (subtree tops).
		referenced := make(map[int]bool)
		inUnreach := make(map[int]bool, len(unreach))
		for _, n := range unreach {
			inUnreach[n] = true
		}
		for _, n := range unreach {
			if img.Inodes[n].Type != TypeDir {
				continue
			}
			for _, e := range img.readEntries(n) {
				if inUnreach[e.Inode] {
					referenced[e.Inode] = true
				}
			}
		}
		var tops []int
		for _, n := range unreach {
			if img.hasContent(n) && !referenced[n] {
				tops = append(tops, n)
			}
		}
		var attach []int
		switch {
		case len(tops) > 0:
			attach = tops
		default:
			// Only cycles remain: attach the smallest inode with content.
			for _, n := range unreach {
				if img.hasContent(n) {
					attach = []int{n}
					break
				}
			}
		}
		if len(attach) == 0 {
			break
		}
		rd, err := ensureRecDir()
		if err != nil {
			return err
		}
		entries := img.readEntries(rd)
		for _, n := range attach {
			name := strconv.Itoa(n)
			entries = append(entries, DirEntry{Inode: n, Name: name})
			log(Action{Kind: "attach-unreachable", Inode: n, Block: -1, Parent: rd, Name: name,
				Detail: "unreachable inode with content attached to recovery directory"})
		}
		if err := img.writeEntries(rd, entries); err != nil {
			return err
		}
		// Attaching a directory must not create a second parent: drop any
		// entry in other directories (cycle back-edges) that points at an
		// attached directory. The rest of the component stays reachable
		// through it. Files may keep multiple parents (hard links).
		for _, n := range attach {
			if img.Inodes[n].Type != TypeDir {
				continue
			}
			for d := range img.Inodes {
				if d == rd || img.Inodes[d].Type != TypeDir {
					continue
				}
				entries := img.readEntries(d)
				kept := entries[:0]
				dirty := false
				for _, e := range entries {
					if e.Inode == n {
						log(Action{Kind: "drop-extra-parent", Inode: n, Block: -1, Parent: d, Name: e.Name,
							Detail: "attached inode keeps the recovery directory as its only parent"})
						dirty = true
						continue
					}
					kept = append(kept, e)
				}
				if dirty {
					if err := img.writeEntries(d, kept); err != nil {
						return err
					}
				}
			}
		}
	}

	// Whatever remains unreachable has no content: free it.
	reach := img.reachable()
	for i := range img.Inodes {
		if img.Inodes[i].Type != TypeFree && !reach[i] {
			log(Action{Kind: "free-unreachable", Inode: i, Block: -1, Parent: -1,
				Detail: "unreachable inode without content released"})
			img.Inodes[i] = Inode{Type: TypeFree}
		}
	}
	return nil
}
