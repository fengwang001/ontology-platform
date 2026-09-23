package dict

import (
	"encoding/binary"
	"errors"
	"os"

	"ontology/block"
)

const fileMagic = "ONDICT1"

// Dict is an immutable, multi-block ordered string dictionary.
type Dict struct {
	blocks []*block.Block
	first  []string // first value of each block
	last   []string // last value of each block
	base   []int    // global index of each block's first entry
}

// Build groups ss into blocks of at most blockSize with restart interval k.
func Build(ss []string, blockSize, k int) (*Dict, error) {
	if blockSize < 1 {
		blockSize = 1
	}
	d := &Dict{}
	for start := 0; start < len(ss); start += blockSize {
		end := start + blockSize
		if end > len(ss) {
			end = len(ss)
		}
		raw, err := block.Encode(ss[start:end], k)
		if err != nil {
			return nil, err
		}
		b, err := block.Parse(raw)
		if err != nil {
			return nil, err
		}
		d.add(b, start)
	}
	return d, nil
}

// add registers a finished block atomically w.r.t. concurrent readers.
func (d *Dict) add(b *block.Block, base int) {
	d.blocks = append(d.blocks, b)
	d.first = append(d.first, b.At(0))
	d.last = append(d.last, b.At(b.Len()-1))
	d.base = append(d.base, base)
}

func (d *Dict) Blocks() []*block.Block { return d.blocks }
func (d *Dict) BlockCount() int        { return len(d.blocks) }
func (d *Dict) Len() int {
	if len(d.blocks) == 0 {
		return 0
	}
	last := d.blocks[len(d.blocks)-1]
	return d.base[len(d.base)-1] + last.Len()
}
func (d *Dict) First(i int) string { return d.first[i] }
func (d *Dict) Last(i int) string  { return d.last[i] }
func (d *Dict) Base(i int) int     { return d.base[i] }

// AppendBlock adds one already encoded block; used to show unfinished
// blocks are invisible: the block is only registered after full parse.
func (d *Dict) AppendBlock(raw []byte) error {
	b, err := block.Parse(raw) // unfinished/corrupt block never registers
	if err != nil {
		return err
	}
	base := 0
	if len(d.base) > 0 {
		base = d.base[len(d.base)-1] + d.blocks[len(d.blocks)-1].Len()
	}
	d.add(b, base)
	return nil
}

var errFileHeader = errors.New("dict: invalid file header")

// Save writes the whole dictionary to path.
func (d *Dict) Save(path string) error {
	out := make([]byte, 0)
	out = append(out, fileMagic...)
	var nb [4]byte
	binary.LittleEndian.PutUint32(nb[:], uint32(len(d.blocks)))
	out = append(out, nb[:]...)
	for _, b := range d.blocks {
		raw := b.Raw()
		var lb [4]byte
		binary.LittleEndian.PutUint32(lb[:], uint32(len(raw)))
		out = append(out, lb[:]...)
		out = append(out, raw...)
	}
	return os.WriteFile(path, out, 0o644)
}

// Load reads a dictionary file.
func Load(path string) (*Dict, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return FromBytes(raw)
}

// FromBytes parses a serialized dictionary.
func FromBytes(raw []byte) (*Dict, error) {
	if len(raw) < len(fileMagic)+4 || string(raw[:len(fileMagic)]) != fileMagic {
		return nil, errFileHeader
	}
	n := int(binary.LittleEndian.Uint32(raw[len(fileMagic):]))
	pos := len(fileMagic) + 4
	d := &Dict{}
	for i := 0; i < n; i++ {
		if pos+4 > len(raw) {
			return nil, errFileHeader
		}
		lb := int(binary.LittleEndian.Uint32(raw[pos:]))
		pos += 4
		if pos+lb > len(raw) {
			return nil, errFileHeader
		}
		if err := d.AppendBlock(raw[pos:pos+lb]); err != nil {
			return nil, err
		}
		pos += lb
	}
	return d, nil
}
