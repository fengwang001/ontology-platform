// Package fsck implements a consistency checker and repairer for a small
// block filesystem image. The image contains a superblock, an inode table,
// a block bitmap and data blocks. Directories are stored as fixed-size
// entries inside the data blocks of directory inodes.
package fsck

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// On-disk layout constants.
const (
	BlockSize       = 512
	InodeSize       = 32
	InodesPerBlock  = BlockSize / InodeSize
	MaxDirect       = 5
	EntrySize       = 32
	EntriesPerBlock = BlockSize / EntrySize
	MaxName         = 27

	// RecoveryName is the name of the recovery directory created under the
	// root directory when unreachable inodes must be re-attached.
	RecoveryName = "lost+found"

	formatVersion = 1
)

// Inode types.
const (
	TypeFree = 0
	TypeFile = 1
	TypeDir  = 2
)

// Superblock flags.
const (
	// FlagMounted marks the image as currently write-mounted.
	FlagMounted = 1
)

var magic = [8]byte{'O', 'N', 'T', 'O', 'F', 'S', '0', '1'}

// Distinguishable refusal reasons.
var (
	ErrCorruptHeader     = errors.New("fsck: image header is corrupt")
	ErrRootNotDir        = errors.New("fsck: root inode is not a directory")
	ErrRecoveryNameTaken = errors.New("fsck: recovery directory name is used by a non-directory")
	ErrNoFreeInode       = errors.New("fsck: recovery directory needed but no free inode remains")
	ErrMounted           = errors.New("fsck: image is write-mounted")
	ErrNoFreeBlock       = errors.New("fsck: no free block available to grow a directory")
	ErrDirFull           = errors.New("fsck: directory has no room for more entries")
)

// Inode is the in-memory form of an inode table slot.
type Inode struct {
	Type   uint8
	Nlink  uint16
	Blocks []uint32
}

// DirEntry is one directory entry.
type DirEntry struct {
	Inode int
	Name  string
}

// Image is a parsed filesystem image.
type Image struct {
	InodeCount int
	BlockCount int
	RootInode  int
	Flags      uint32
	Inodes     []Inode
	Bitmap     []bool
	Blocks     [][]byte
}

func inodeBlocks(inodeCount int) int {
	return (inodeCount + InodesPerBlock - 1) / InodesPerBlock
}

func bitmapBlocks(blockCount int) int {
	return (blockCount + BlockSize*8 - 1) / (BlockSize * 8)
}

// Layout returns the byte offsets of the inode table, the bitmap and the
// data region, plus the total image size.
func Layout(inodeCount, blockCount int) (inodeOff, bitmapOff, dataOff, total int) {
	inodeOff = BlockSize
	bitmapOff = inodeOff + inodeBlocks(inodeCount)*BlockSize
	dataOff = bitmapOff + bitmapBlocks(blockCount)*BlockSize
	total = dataOff + blockCount*BlockSize
	return
}

// ParseImage decodes an image from its on-disk byte form.
func ParseImage(data []byte) (*Image, error) {
	if len(data) < BlockSize {
		return nil, ErrCorruptHeader
	}
	var m [8]byte
	copy(m[:], data[:8])
	if m != magic {
		return nil, ErrCorruptHeader
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != formatVersion {
		return nil, ErrCorruptHeader
	}
	inodeCount := int(binary.LittleEndian.Uint32(data[12:16]))
	blockCount := int(binary.LittleEndian.Uint32(data[16:20]))
	rootInode := int(binary.LittleEndian.Uint32(data[20:24]))
	flags := binary.LittleEndian.Uint32(data[24:28])
	if inodeCount <= 0 || blockCount <= 0 || rootInode < 0 || rootInode >= inodeCount {
		return nil, ErrCorruptHeader
	}
	inodeOff, bitmapOff, dataOff, total := Layout(inodeCount, blockCount)
	if len(data) != total {
		return nil, ErrCorruptHeader
	}

	img := &Image{
		InodeCount: inodeCount,
		BlockCount: blockCount,
		RootInode:  rootInode,
		Flags:      flags,
		Inodes:     make([]Inode, inodeCount),
		Bitmap:     make([]bool, blockCount),
		Blocks:     make([][]byte, blockCount),
	}
	for i := 0; i < inodeCount; i++ {
		off := inodeOff + i*InodeSize
		typ := data[off]
		nlink := binary.LittleEndian.Uint16(data[off+2 : off+4])
		nblocks := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		if nblocks > MaxDirect {
			nblocks = MaxDirect
		}
		blocks := make([]uint32, nblocks)
		for j := range blocks {
			blocks[j] = binary.LittleEndian.Uint32(data[off+8+j*4 : off+12+j*4])
		}
		img.Inodes[i] = Inode{Type: typ, Nlink: nlink, Blocks: blocks}
	}
	for b := 0; b < blockCount; b++ {
		img.Bitmap[b] = data[bitmapOff+b/8]&(1<<uint(b%8)) != 0
	}
	for b := 0; b < blockCount; b++ {
		blk := make([]byte, BlockSize)
		copy(blk, data[dataOff+b*BlockSize:dataOff+(b+1)*BlockSize])
		img.Blocks[b] = blk
	}
	return img, nil
}

// Bytes serializes the image deterministically.
func (img *Image) Bytes() []byte {
	inodeOff, bitmapOff, dataOff, total := Layout(img.InodeCount, img.BlockCount)
	out := make([]byte, total)
	copy(out[:8], magic[:])
	binary.LittleEndian.PutUint32(out[8:12], formatVersion)
	binary.LittleEndian.PutUint32(out[12:16], uint32(img.InodeCount))
	binary.LittleEndian.PutUint32(out[16:20], uint32(img.BlockCount))
	binary.LittleEndian.PutUint32(out[20:24], uint32(img.RootInode))
	binary.LittleEndian.PutUint32(out[24:28], img.Flags)
	for i, in := range img.Inodes {
		off := inodeOff + i*InodeSize
		out[off] = in.Type
		binary.LittleEndian.PutUint16(out[off+2:off+4], in.Nlink)
		binary.LittleEndian.PutUint32(out[off+4:off+8], uint32(len(in.Blocks)))
		for j, b := range in.Blocks {
			binary.LittleEndian.PutUint32(out[off+8+j*4:off+12+j*4], b)
		}
	}
	for b := 0; b < img.BlockCount; b++ {
		if img.Bitmap[b] {
			out[bitmapOff+b/8] |= 1 << uint(b%8)
		}
	}
	for b := 0; b < img.BlockCount; b++ {
		copy(out[dataOff+b*BlockSize:dataOff+(b+1)*BlockSize], img.Blocks[b])
	}
	return out
}

// Clone returns a deep copy of the image.
func (img *Image) Clone() *Image {
	cp := &Image{
		InodeCount: img.InodeCount,
		BlockCount: img.BlockCount,
		RootInode:  img.RootInode,
		Flags:      img.Flags,
		Inodes:     make([]Inode, len(img.Inodes)),
		Bitmap:     make([]bool, len(img.Bitmap)),
		Blocks:     make([][]byte, len(img.Blocks)),
	}
	for i, in := range img.Inodes {
		blocks := make([]uint32, len(in.Blocks))
		copy(blocks, in.Blocks)
		cp.Inodes[i] = Inode{Type: in.Type, Nlink: in.Nlink, Blocks: blocks}
	}
	copy(cp.Bitmap, img.Bitmap)
	for b, blk := range img.Blocks {
		nb := make([]byte, len(blk))
		copy(nb, blk)
		cp.Blocks[b] = nb
	}
	return cp
}

// readEntries decodes all entries of a directory inode in slot order.
func (img *Image) readEntries(dir int) []DirEntry {
	var entries []DirEntry
	for _, b := range img.Inodes[dir].Blocks {
		if int(b) >= img.BlockCount {
			continue
		}
		blk := img.Blocks[b]
		for s := 0; s < EntriesPerBlock; s++ {
			off := s * EntrySize
			nameLen := int(blk[off+4])
			if nameLen == 0 || nameLen > MaxName {
				continue
			}
			entries = append(entries, DirEntry{
				Inode: int(binary.LittleEndian.Uint32(blk[off : off+4])),
				Name:  string(blk[off+5 : off+5+nameLen]),
			})
		}
	}
	return entries
}

// usedBlocks returns the set of data blocks referenced by allocated inodes.
func (img *Image) usedBlocks() map[int]bool {
	used := make(map[int]bool)
	for _, in := range img.Inodes {
		if in.Type == TypeFree {
			continue
		}
		for _, b := range in.Blocks {
			if int(b) < img.BlockCount {
				used[int(b)] = true
			}
		}
	}
	return used
}

// lowestFreeBlock returns the lowest-numbered data block not referenced by
// any allocated inode, or -1 when none exists.
func (img *Image) lowestFreeBlock() int {
	used := img.usedBlocks()
	for b := 0; b < img.BlockCount; b++ {
		if !used[b] {
			return b
		}
	}
	return -1
}

// writeEntries stores entries into the directory inode, growing it with the
// lowest-numbered free block when necessary. Stale slots are cleared.
func (img *Image) writeEntries(dir int, entries []DirEntry) error {
	in := &img.Inodes[dir]
	for len(entries) > len(in.Blocks)*EntriesPerBlock {
		if len(in.Blocks) >= MaxDirect {
			return ErrDirFull
		}
		b := img.lowestFreeBlock()
		if b < 0 {
			return ErrNoFreeBlock
		}
		img.Blocks[b] = make([]byte, BlockSize)
		in.Blocks = append(in.Blocks, uint32(b))
	}
	for _, blk := range in.Blocks {
		if int(blk) >= img.BlockCount {
			continue
		}
		buf := img.Blocks[blk]
		for j := range buf {
			buf[j] = 0
		}
	}
	idx := 0
	for _, blk := range in.Blocks {
		if int(blk) >= img.BlockCount {
			continue
		}
		buf := img.Blocks[blk]
		for s := 0; s < EntriesPerBlock && idx < len(entries); s++ {
			e := entries[idx]
			if len(e.Name) > MaxName {
				return fmt.Errorf("fsck: entry name %q too long", e.Name)
			}
			off := s * EntrySize
			binary.LittleEndian.PutUint32(buf[off:off+4], uint32(e.Inode))
			buf[off+4] = byte(len(e.Name))
			copy(buf[off+5:off+5+len(e.Name)], e.Name)
			idx++
		}
	}
	return nil
}
