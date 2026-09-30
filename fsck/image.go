// Package fsck implements a consistency checker and repairer for a simple
// block filesystem image. The image contains a superblock, an inode table,
// a block bitmap and data blocks. Directories are regular data blocks
// filled with fixed-size directory entries.
package fsck

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
)

const (
	// Magic identifies a valid image header.
	Magic = "ONTOFS01"
	// Version is the only supported on-disk format version.
	Version = 1

	// SuperblockSize is the byte size of the superblock at block 0.
	SuperblockSize = 64
	// InodeSize is the byte size of one inode table entry.
	InodeSize = 64
	// DirEntrySize is the byte size of one directory entry.
	DirEntrySize = 32
	// NameMax is the maximum directory entry name length in bytes.
	NameMax = 28
	// DirectBlocks is the number of direct block pointers per inode.
	DirectBlocks = 12

	// RootInode is the fixed inode number of the root directory.
	RootInode = 0
	// RecoveryDirName is the name of the recovery directory under root.
	RecoveryDirName = "lost+found"
)

// Inode types stored in Inode.Type.
const (
	InodeFree = 0
	InodeFile = 1
	InodeDir  = 2
)

// Superblock describes the static layout of an image.
type Superblock struct {
	BlockSize        uint32
	TotalBlocks      uint32
	InodeCount       uint32
	InodeTableStart  uint32
	InodeTableBlocks uint32
	BitmapStart      uint32
	BitmapBlocks     uint32
	DataStart        uint32
}

// Inode is one in-memory inode table entry.
type Inode struct {
	Type   uint8
	Nlink  uint32
	Size   uint32
	Blocks [DirectBlocks]uint32
}

// DirEntry is one directory entry.
type DirEntry struct {
	Inode uint32
	Name  string
}

// Image is a fully in-memory representation of a filesystem image.
type Image struct {
	SB     Superblock
	Inodes []Inode
	Bitmap []bool // len == TotalBlocks; metadata blocks always used
	Data   [][]byte
}

// DataBlocks returns the number of data blocks in the image.
func (img *Image) DataBlocks() uint32 {
	return img.SB.TotalBlocks - img.SB.DataStart
}

// InRange reports whether b is a valid data block number.
func (img *Image) InRange(b uint32) bool {
	return b >= img.SB.DataStart && b < img.SB.TotalBlocks
}

// BlockData returns the raw content of data block b. Callers must ensure
// InRange(b) holds.
func (img *Image) BlockData(b uint32) []byte {
	return img.Data[b-img.SB.DataStart]
}

// Allocated reports whether inode i is allocated.
func (img *Image) Allocated(i uint32) bool {
	return i < uint32(len(img.Inodes)) && img.Inodes[i].Type != InodeFree
}

// Format creates a fresh empty image with a root directory.
func Format(blockSize, totalBlocks, inodeCount uint32) (*Image, error) {
	if blockSize < SuperblockSize {
		return nil, fmt.Errorf("fsck: block size %d too small", blockSize)
	}
	if inodeCount < 1 {
		return nil, fmt.Errorf("fsck: inode count must be >= 1")
	}
	inodeTableBlocks := ceilDiv(inodeCount*InodeSize, blockSize)
	bitmapBlocks := ceilDiv(ceilDiv(totalBlocks, 8), blockSize)
	dataStart := 1 + inodeTableBlocks + bitmapBlocks
	if dataStart >= totalBlocks {
		return nil, fmt.Errorf("fsck: image too small: %d blocks", totalBlocks)
	}
	img := &Image{
		SB: Superblock{
			BlockSize:        blockSize,
			TotalBlocks:      totalBlocks,
			InodeCount:       inodeCount,
			InodeTableStart:  1,
			InodeTableBlocks: inodeTableBlocks,
			BitmapStart:      1 + inodeTableBlocks,
			BitmapBlocks:     bitmapBlocks,
			DataStart:        dataStart,
		},
		Inodes: make([]Inode, inodeCount),
		Bitmap: make([]bool, totalBlocks),
		Data:   make([][]byte, totalBlocks-dataStart),
	}
	for i := range img.Data {
		img.Data[i] = make([]byte, blockSize)
	}
	for b := uint32(0); b < dataStart; b++ {
		img.Bitmap[b] = true
	}
	img.Inodes[RootInode] = Inode{Type: InodeDir, Nlink: 1}
	return img, nil
}

func ceilDiv(a, b uint32) uint32 {
	return (a + b - 1) / b
}

// Serialize renders the image to its on-disk byte representation.
// The output is deterministic for a given Image.
func (img *Image) Serialize() []byte {
	bs := int(img.SB.BlockSize)
	raw := make([]byte, int(img.SB.TotalBlocks)*bs)

	copy(raw[0:8], Magic)
	put32(raw[8:], Version)
	put32(raw[12:], img.SB.BlockSize)
	put32(raw[16:], img.SB.TotalBlocks)
	put32(raw[20:], img.SB.InodeCount)
	put32(raw[24:], img.SB.InodeTableStart)
	put32(raw[28:], img.SB.InodeTableBlocks)
	put32(raw[32:], img.SB.BitmapStart)
	put32(raw[36:], img.SB.BitmapBlocks)
	put32(raw[40:], img.SB.DataStart)
	put32(raw[48:], crc32.ChecksumIEEE(raw[0:48]))

	off := int(img.SB.InodeTableStart) * bs
	for _, in := range img.Inodes {
		raw[off] = in.Type
		put32(raw[off+4:], in.Nlink)
		put32(raw[off+8:], in.Size)
		for i, b := range in.Blocks {
			put32(raw[off+12+i*4:], b)
		}
		off += InodeSize
	}

	boff := int(img.SB.BitmapStart) * bs
	for i, used := range img.Bitmap {
		if used {
			raw[boff+i/8] |= 1 << (uint(i) % 8)
		}
	}

	doff := int(img.SB.DataStart) * bs
	for i, blk := range img.Data {
		copy(raw[doff+i*bs:], blk)
	}
	return raw
}

// Parse loads an image from its on-disk byte representation.
// Any header inconsistency yields ErrCorruptHeader.
func Parse(raw []byte) (*Image, error) {
	if len(raw) < SuperblockSize {
		return nil, ErrCorruptHeader
	}
	if string(raw[0:8]) != Magic {
		return nil, ErrCorruptHeader
	}
	if get32(raw[8:]) != Version {
		return nil, ErrCorruptHeader
	}
	if crc32.ChecksumIEEE(raw[0:48]) != get32(raw[48:]) {
		return nil, ErrCorruptHeader
	}
	sb := Superblock{
		BlockSize:        get32(raw[12:]),
		TotalBlocks:      get32(raw[16:]),
		InodeCount:       get32(raw[20:]),
		InodeTableStart:  get32(raw[24:]),
		InodeTableBlocks: get32(raw[28:]),
		BitmapStart:      get32(raw[32:]),
		BitmapBlocks:     get32(raw[36:]),
		DataStart:        get32(raw[40:]),
	}
	if sb.BlockSize < SuperblockSize || sb.BlockSize > 1<<20 ||
		sb.TotalBlocks == 0 || sb.InodeCount == 0 {
		return nil, ErrCorruptHeader
	}
	if sb.InodeTableStart < 1 ||
		sb.BitmapStart < sb.InodeTableStart+sb.InodeTableBlocks ||
		sb.DataStart < sb.BitmapStart+sb.BitmapBlocks ||
		sb.DataStart > sb.TotalBlocks {
		return nil, ErrCorruptHeader
	}
	if uint64(sb.InodeTableBlocks)*uint64(sb.BlockSize) < uint64(sb.InodeCount)*InodeSize {
		return nil, ErrCorruptHeader
	}
	if uint64(sb.BitmapBlocks)*uint64(sb.BlockSize)*8 < uint64(sb.TotalBlocks) {
		return nil, ErrCorruptHeader
	}
	if uint64(len(raw)) != uint64(sb.TotalBlocks)*uint64(sb.BlockSize) {
		return nil, ErrCorruptHeader
	}

	bs := int(sb.BlockSize)
	img := &Image{
		SB:     sb,
		Inodes: make([]Inode, sb.InodeCount),
		Bitmap: make([]bool, sb.TotalBlocks),
		Data:   make([][]byte, sb.TotalBlocks-sb.DataStart),
	}
	off := int(sb.InodeTableStart) * bs
	for i := range img.Inodes {
		in := &img.Inodes[i]
		in.Type = raw[off]
		in.Nlink = get32(raw[off+4:])
		in.Size = get32(raw[off+8:])
		for j := range in.Blocks {
			in.Blocks[j] = get32(raw[off+12+j*4:])
		}
		off += InodeSize
	}
	boff := int(sb.BitmapStart) * bs
	for i := range img.Bitmap {
		img.Bitmap[i] = raw[boff+i/8]&(1<<(uint(i)%8)) != 0
	}
	doff := int(sb.DataStart) * bs
	for i := range img.Data {
		img.Data[i] = make([]byte, bs)
		copy(img.Data[i], raw[doff+i*bs:doff+(i+1)*bs])
	}
	return img, nil
}

func put32(dst []byte, v uint32) {
	binary.LittleEndian.PutUint32(dst, v)
}

func get32(src []byte) uint32 {
	return binary.LittleEndian.Uint32(src)
}
