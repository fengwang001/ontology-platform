package fsck

import (
	"os"
	"path/filepath"
	"testing"
)

// inodeSpec describes one inode slot in a test image. nlink < 0 means the
// builder derives the link count from the directory entries.
type inodeSpec struct {
	typ    uint8
	blocks []int
	nlink  int
}

func file(blocks ...int) inodeSpec { return inodeSpec{typ: TypeFile, blocks: blocks, nlink: -1} }
func dir(blocks ...int) inodeSpec  { return inodeSpec{typ: TypeDir, blocks: blocks, nlink: -1} }
func (s inodeSpec) links(n int) inodeSpec {
	s.nlink = n
	return s
}

type dent struct {
	name   string
	target int
}

// imageSpec is a declarative description of a test image. The builder
// produces a structurally consistent image (correct nlink and bitmap)
// unless the spec explicitly overrides nlink or flips bitmap bits.
type imageSpec struct {
	inodeCount int
	blockCount int
	root       int
	flags      uint32
	inodes     map[int]inodeSpec
	entries    map[int][]dent
	blockData  map[int]string
	bitmapFlip []int
}

func buildImage(t *testing.T, spec imageSpec) []byte {
	t.Helper()
	img := &Image{
		InodeCount: spec.inodeCount,
		BlockCount: spec.blockCount,
		RootInode:  spec.root,
		Flags:      spec.flags,
		Inodes:     make([]Inode, spec.inodeCount),
		Bitmap:     make([]bool, spec.blockCount),
		Blocks:     make([][]byte, spec.blockCount),
	}
	for b := range img.Blocks {
		img.Blocks[b] = make([]byte, BlockSize)
	}
	for i, s := range spec.inodes {
		blocks := make([]uint32, len(s.blocks))
		for j, b := range s.blocks {
			blocks[j] = uint32(b)
		}
		img.Inodes[i] = Inode{Type: s.typ, Blocks: blocks}
	}
	counts := map[int]int{}
	for _, es := range spec.entries {
		for _, e := range es {
			counts[e.target]++
		}
	}
	for d, es := range spec.entries {
		entries := make([]DirEntry, len(es))
		for i, e := range es {
			entries[i] = DirEntry{Inode: e.target, Name: e.name}
		}
		if err := img.writeEntries(d, entries); err != nil {
			t.Fatalf("build image: %v", err)
		}
	}
	for i, s := range spec.inodes {
		n := counts[i]
		if s.nlink >= 0 {
			n = s.nlink
		}
		img.Inodes[i].Nlink = uint16(n)
	}
	for b, content := range spec.blockData {
		copy(img.Blocks[b], content)
	}
	used := img.usedBlocks()
	for b := 0; b < spec.blockCount; b++ {
		img.Bitmap[b] = used[b]
	}
	for _, b := range spec.bitmapFlip {
		img.Bitmap[b] = !img.Bitmap[b]
	}
	return img.Bytes()
}

func newChecker(t *testing.T, data []byte) *Checker {
	t.Helper()
	path := filepath.Join(t.TempDir(), "image.fs")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return Open(path)
}

func readImageFile(t *testing.T, c *Checker) []byte {
	t.Helper()
	data, err := os.ReadFile(c.Path())
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func parseImageFile(t *testing.T, c *Checker) *Image {
	t.Helper()
	img, err := ParseImage(readImageFile(t, c))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func entryNames(img *Image, dir int) map[string]int {
	out := map[string]int{}
	for _, e := range img.readEntries(dir) {
		out[e.Name] = e.Inode
	}
	return out
}
