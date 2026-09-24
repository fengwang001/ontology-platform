// Package sparse 实现每 N 条一个锚点的稀疏偏移索引。
// 文件布局：magic "AIDX"(4) + interval(8) + count(8) + count*[seq(8) offset(8)]。
package sparse

import (
	"encoding/binary"
	"errors"
	"os"

	"ontology/segment"
)

const magicText = "AIDX"

var (
	// ErrBadIndex 索引文件不完整或魔数不符。
	ErrBadIndex = errors.New("sparse: bad index file")
	// ErrEmpty 索引为空。
	ErrEmpty = errors.New("sparse: empty index")
)

// Anchor 是一个「序号 -> 记录起始偏移」锚点。
type Anchor struct {
	Seq    uint64
	Offset int64
}

// Index 是不可变的稀疏索引视图。
type Index struct {
	Interval uint64
	anchors  []Anchor
}

// New 从锚点构造索引。
func New(interval uint64, anchors []Anchor) *Index {
	cp := make([]Anchor, len(anchors))
	copy(cp, anchors)
	return &Index{Interval: interval, anchors: cp}
}

// Anchors 返回锚点副本。
func (x *Index) Anchors() []Anchor {
	out := make([]Anchor, len(x.anchors))
	copy(out, x.anchors)
	return out
}

// Lookup 返回「不大于 from 的最大锚点」。
// 若所有锚点都大于 from，则返回 ErrEmpty（调用方应从段头开始扫描）。
func (x *Index) Lookup(from uint64) (Anchor, error) {
	if len(x.anchors) == 0 {
		return Anchor{}, ErrEmpty
	}
	lo, hi := 0, len(x.anchors)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if x.anchors[mid].Seq <= from {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return Anchor{}, ErrEmpty
	}
	return x.anchors[lo-1], nil
}

// Marshal 把索引序列化为可逐字节复现的字节串。
func (x *Index) Marshal() []byte {
	b := make([]byte, 20+16*len(x.anchors))
	copy(b[0:4], magicText)
	binary.LittleEndian.PutUint64(b[4:12], x.Interval)
	binary.LittleEndian.PutUint64(b[12:20], uint64(len(x.anchors)))
	for i, a := range x.anchors {
		base := 20 + 16*i
		binary.LittleEndian.PutUint64(b[base:base+8], a.Seq)
		binary.LittleEndian.PutUint64(b[base+8:base+16], uint64(a.Offset))
	}
	return b
}

// Unmarshal 解析索引字节串。
func Unmarshal(b []byte) (*Index, error) {
	if len(b) < 20 || string(b[:4]) != magicText {
		return nil, ErrBadIndex
	}
	interval := binary.LittleEndian.Uint64(b[4:12])
	count := binary.LittleEndian.Uint64(b[12:20])
	if uint64(len(b)) < 20+16*count {
		return nil, ErrBadIndex
	}
	anchors := make([]Anchor, count)
	for i := range anchors {
		base := 20 + 16*i
		anchors[i] = Anchor{
			Seq:    binary.LittleEndian.Uint64(b[base : base+8]),
			Offset: int64(binary.LittleEndian.Uint64(b[base+8 : base+16])),
		}
	}
	return &Index{Interval: interval, anchors: anchors}, nil
}

// Load 读取段对应的索引文件；不存在时返回 os.ErrNotExist。
func Load(logPath string) (*Index, error) {
	b, err := os.ReadFile(segment.IndexPath(logPath))
	if err != nil {
		return nil, err
	}
	return Unmarshal(b)
}

// Save 原子地（同目录临时文件 + rename）写入索引。
func (x *Index) Save(logPath string) error {
	idxPath := segment.IndexPath(logPath)
	tmp := idxPath + ".tmp"
	if err := os.WriteFile(tmp, x.Marshal(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, idxPath)
}
