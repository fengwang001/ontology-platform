package fsck

// EntryPos identifies one directory entry slot inside a directory's data.
type EntryPos struct {
	Block uint32
	Slot  int
}

// entryPositions lists all entry slots of a directory in block-list order.
func (img *Image) entryPositions(dir uint32) []EntryPos {
	in := &img.Inodes[dir]
	slotsPerBlock := int(img.SB.BlockSize) / DirEntrySize
	var out []EntryPos
	for _, b := range in.Blocks {
		if b == 0 || !img.InRange(b) {
			continue
		}
		for s := 0; s < slotsPerBlock; s++ {
			out = append(out, EntryPos{Block: b, Slot: s})
		}
	}
	return out
}

// readEntry reads the entry at pos.
func (img *Image) readEntry(pos EntryPos) DirEntry {
	data := img.BlockData(pos.Block)
	off := pos.Slot * DirEntrySize
	ino := get32(data[off:])
	nameBytes := data[off+4 : off+4+NameMax]
	end := 0
	for end < len(nameBytes) && nameBytes[end] != 0 {
		end++
	}
	return DirEntry{Inode: ino, Name: string(nameBytes[:end])}
}

// writeEntry stores e at pos.
func (img *Image) writeEntry(pos EntryPos, e DirEntry) {
	data := img.BlockData(pos.Block)
	off := pos.Slot * DirEntrySize
	put32(data[off:], e.Inode)
	for i := 0; i < NameMax; i++ {
		data[off+4+i] = 0
	}
	name := e.Name
	if len(name) > NameMax {
		name = name[:NameMax]
	}
	copy(data[off+4:], name)
}

// clearEntry empties the slot at pos.
func (img *Image) clearEntry(pos EntryPos) {
	img.writeEntry(pos, DirEntry{})
}

// Entries returns the non-empty entries of directory dir in slot order.
func (img *Image) Entries(dir uint32) []DirEntry {
	var out []DirEntry
	for _, pos := range img.entryPositions(dir) {
		e := img.readEntry(pos)
		if e.Inode != 0 || e.Name != "" {
			out = append(out, e)
		}
	}
	return out
}

// findEntry locates an entry by name in directory dir.
func (img *Image) findEntry(dir uint32, name string) (DirEntry, bool) {
	for _, pos := range img.entryPositions(dir) {
		e := img.readEntry(pos)
		if e.Name == name && e.Inode != 0 {
			return e, true
		}
	}
	return DirEntry{}, false
}

// usedBlockSet returns the set of in-range data blocks referenced by any
// allocated inode.
func (img *Image) usedBlockSet() map[uint32]bool {
	used := make(map[uint32]bool)
	for i := range img.Inodes {
		if img.Inodes[i].Type == InodeFree {
			continue
		}
		for _, b := range img.Inodes[i].Blocks {
			if b != 0 && img.InRange(b) {
				used[b] = true
			}
		}
	}
	return used
}

// lowestFreeBlock returns the lowest-numbered unreferenced data block.
func (img *Image) lowestFreeBlock(used map[uint32]bool) (uint32, bool) {
	for b := img.SB.DataStart; b < img.SB.TotalBlocks; b++ {
		if !used[b] {
			return b, true
		}
	}
	return 0, false
}

// addEntry inserts e into directory dir, growing the directory by one data
// block when every existing slot is occupied.
func (img *Image) addEntry(dir uint32, e DirEntry) error {
	for _, pos := range img.entryPositions(dir) {
		cur := img.readEntry(pos)
		if cur.Inode == 0 && cur.Name == "" {
			img.writeEntry(pos, e)
			return nil
		}
	}
	in := &img.Inodes[dir]
	slot := -1
	for i, b := range in.Blocks {
		if b == 0 {
			slot = i
			break
		}
	}
	if slot < 0 {
		return ErrNoSpace
	}
	used := img.usedBlockSet()
	free, ok := img.lowestFreeBlock(used)
	if !ok {
		return ErrNoSpace
	}
	in.Blocks[slot] = free
	img.writeEntry(EntryPos{Block: free, Slot: 0}, e)
	return nil
}

// removeEntriesIf clears every slot of dir whose entry satisfies pred and
// returns the removed entries in slot order.
func (img *Image) removeEntriesIf(dir uint32, pred func(DirEntry) bool) []DirEntry {
	var removed []DirEntry
	for _, pos := range img.entryPositions(dir) {
		e := img.readEntry(pos)
		if (e.Inode != 0 || e.Name != "") && pred(e) {
			img.clearEntry(pos)
			removed = append(removed, e)
		}
	}
	return removed
}

// hasContent reports whether inode i holds any data: a file with at least
// one block, or a directory with at least one entry.
func (img *Image) hasContent(i uint32) bool {
	in := &img.Inodes[i]
	switch in.Type {
	case InodeFile:
		for _, b := range in.Blocks {
			if b != 0 {
				return true
			}
		}
	case InodeDir:
		return len(img.Entries(i)) > 0
	}
	return false
}
