// Package sparse 实现稀疏索引：每 N 条事件记一个「序号 → 字节偏移」锚点。
package sparse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"

	"ontology/event"
	"ontology/segment"
)

const (
	// HeaderSize 是索引头字节数：magic4 + interval4 + numAnchors4。
	HeaderSize = 12
	// AnchorSize 是单条锚点字节数：seq8 + offset8。
	AnchorSize = 16
)

var magic = [4]byte{'O', 'S', 'I', 'X'}

// ErrBadIndex 表示索引文件本身无法解析。
var ErrBadIndex = errors.New("sparse: bad index file")

// Anchor 是一个「序号 → 字节偏移」锚点。
type Anchor struct {
	Seq    uint64
	Offset uint64
}

// Index 是一个段的稀疏索引。
type Index struct {
	N       uint32
	Anchors []Anchor
}

// Encode 序列化索引（字节布局即磁盘格式）。
func (idx *Index) Encode() []byte {
	buf := make([]byte, HeaderSize+len(idx.Anchors)*AnchorSize)
	copy(buf[0:4], magic[:])
	binary.LittleEndian.PutUint32(buf[4:8], idx.N)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(len(idx.Anchors)))
	for i, a := range idx.Anchors {
		o := HeaderSize + i*AnchorSize
		binary.LittleEndian.PutUint64(buf[o:o+8], a.Seq)
		binary.LittleEndian.PutUint64(buf[o+8:o+16], a.Offset)
	}
	return buf
}

// Decode 反序列化索引。
func Decode(buf []byte) (*Index, error) {
	if len(buf) < HeaderSize || string(buf[0:4]) != string(magic[:]) {
		return nil, fmt.Errorf("%w: header", ErrBadIndex)
	}
	n := binary.LittleEndian.Uint32(buf[4:8])
	cnt := int(binary.LittleEndian.Uint32(buf[8:12]))
	if len(buf) != HeaderSize+cnt*AnchorSize {
		return nil, fmt.Errorf("%w: size %d for %d anchors", ErrBadIndex, len(buf), cnt)
	}
	idx := &Index{N: n, Anchors: make([]Anchor, cnt)}
	for i := range idx.Anchors {
		o := HeaderSize + i*AnchorSize
		idx.Anchors[i] = Anchor{
			Seq:    binary.LittleEndian.Uint64(buf[o : o+8]),
			Offset: binary.LittleEndian.Uint64(buf[o+8 : o+16]),
		}
	}
	return idx, nil
}

// Build 顺序扫描段文件构建索引并写入 idxPath。
// 索引内容完全来自段：第 i 条事件 i%N==0 时记 (seq, offset)。
func Build(segPath, idxPath string, n uint32) error {
	f, err := os.Open(segPath)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := segment.ReadHeader(f); err != nil {
		return err
	}
	idx := &Index{N: n}
	var i uint64
	err = segment.Scan(f, segment.HeaderSize, func(off int64, e event.Event, _ int) error {
		if i%uint64(n) == 0 {
			idx.Anchors = append(idx.Anchors, Anchor{Seq: e.Seq, Offset: uint64(off)})
		}
		i++
		return nil
	})
	if err != nil {
		return err
	}
	return os.WriteFile(idxPath, idx.Encode(), 0o644)
}

// Load 读取索引文件。
func Load(path string) (*Index, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Decode(buf)
}

// Locate 返回「不大于 from 的最大锚点」；ok=false 表示 from 小于首锚点。
func (idx *Index) Locate(from uint64) (a Anchor, ok bool) {
	i := sort.Search(len(idx.Anchors), func(i int) bool {
		return idx.Anchors[i].Seq > from
	})
	if i == 0 {
		return Anchor{}, false
	}
	return idx.Anchors[i-1], true
}
