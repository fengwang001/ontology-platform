// Package dict 把有序字符串切成多个前缀压缩块，维护块级首末值目录，
// 字典只读、可整体落盘，块按需惰性解码。
package dict

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sync/atomic"

	"ontology/block"
)

var ErrHeader = errors.New("dict: header incomplete or bad magic")

const headerSize, magic = 20, "PDIC"

var blockDecodes atomic.Int64

// BlockDecodes 返回累计被解压的块数（scan/lookup 测试据此断言）。
func BlockDecodes() int64 { return blockDecodes.Load() }

// ResetBlockDecodes 清零块解压计数器。
func ResetBlockDecodes() { blockDecodes.Store(0) }

// Dict 是只读有序字典；构建完成后不可变，可安全并发查询。
type Dict struct {
	data      []byte
	K         int
	BlockSize int
	Count     int
	offsets   []int
	lengths   []int
	first     []string
	last      []string
}

// Build 把严格递增的 entries 按 blockSize 切块、按 K 设重启点构建字典。
func Build(entries []string, blockSize, k int) (*Dict, error) {
	if blockSize < 1 {
		blockSize = 1
	}
	nBlocks := (len(entries) + blockSize - 1) / blockSize
	var dir, blobs []byte
	var offsets, lengths []int
	for b := 0; b < nBlocks; b++ {
		lo, hi := b*blockSize, min((b+1)*blockSize, len(entries))
		data, err := block.Encode(entries[lo:hi], k)
		if err != nil {
			return nil, fmt.Errorf("dict: block %d: %w", b, err)
		}
		first, last := entries[lo], entries[hi-1]
		offsets = append(offsets, len(blobs))
		lengths = append(lengths, len(data))
		blobs = append(blobs, data...)
		dir = binary.LittleEndian.AppendUint32(dir, uint32(len(data)))
		dir = binary.AppendUvarint(dir, uint64(len(first)))
		dir = append(dir, first...)
		dir = binary.AppendUvarint(dir, uint64(len(last)))
		dir = append(dir, last...)
	}
	buf := make([]byte, 0, headerSize+len(dir)+len(blobs))
	buf = append(buf, magic...)
	for _, v := range []uint32{uint32(k), uint32(blockSize), uint32(len(entries)), uint32(nBlocks)} {
		buf = binary.LittleEndian.AppendUint32(buf, v)
	}
	base := headerSize + len(dir)
	for i, off := range offsets {
		offsets[i] = base + off
	}
	buf = append(buf, dir...)
	buf = append(buf, blobs...)
	return Open(buf)
}

// Open 解析字典头部与目录（块数据惰性解码），不复制 data。
func Open(data []byte) (*Dict, error) {
	if len(data) < headerSize || string(data[:4]) != magic {
		return nil, ErrHeader
	}
	d := &Dict{data: data}
	d.K = int(binary.LittleEndian.Uint32(data[4:]))
	d.BlockSize = int(binary.LittleEndian.Uint32(data[8:]))
	d.Count = int(binary.LittleEndian.Uint32(data[12:]))
	nBlocks := int(binary.LittleEndian.Uint32(data[16:]))
	pos := headerSize
	var lengths []int
	for b := 0; b < nBlocks; b++ {
		if len(data)-pos < 4 {
			return nil, ErrHeader
		}
		lengths = append(lengths, int(binary.LittleEndian.Uint32(data[pos:])))
		pos += 4
		for _, dst := range []*string{ptrAppend(&d.first), ptrAppend(&d.last)} {
			n, m := binary.Uvarint(data[pos:])
			if m <= 0 || uint64(len(data)-pos-m) < n {
				return nil, ErrHeader
			}
			*dst = string(data[pos+m : pos+m+int(n)])
			pos += m + int(n)
		}
	}
	for _, ln := range lengths {
		if ln < 0 || len(data)-pos < ln {
			return nil, ErrHeader
		}
		d.offsets = append(d.offsets, pos)
		d.lengths = append(d.lengths, ln)
		pos += ln
	}
	return d, nil
}

func ptrAppend(s *[]string) *string {
	*s = append(*s, "")
	return &(*s)[len(*s)-1]
}

// Serialize 返回字典的落盘字节表示。
func (d *Dict) Serialize() []byte { return d.data }

// NumBlocks 返回块数。
func (d *Dict) NumBlocks() int { return len(d.offsets) }

// BlockFirst/BlockLast 返回第 b 块的首/末值（来自目录，不解压块）。
func (d *Dict) BlockFirst(b int) string { return d.first[b] }
func (d *Dict) BlockLast(b int) string  { return d.last[b] }

// BlockCount 返回第 b 块的条目数。
func (d *Dict) BlockCount(b int) int {
	return min(d.BlockSize, d.Count-b*d.BlockSize)
}

// BlockData 返回第 b 块的原始字节。
func (d *Dict) BlockData(b int) []byte { return d.data[d.offsets[b] : d.offsets[b]+d.lengths[b]] }

// DecodeBlock 解压第 b 块并校验，累计块解压计数。
func (d *Dict) DecodeBlock(b int) ([]string, error) {
	blockDecodes.Add(1)
	return block.Decode(d.BlockData(b))
}

// Entry 返回全局第 idx 条及其块内解压次数。
func (d *Dict) Entry(idx int) (string, int, error) {
	return block.DecodeEntry(d.BlockData(idx/d.BlockSize), idx%d.BlockSize)
}
