// Package sparse 实现稀疏索引：每 N 条事件记录一个「序号 → 字节偏移」锚点。
//
// 索引是纯加速结构，内容全部可从段文件推导（锚点序号 = firstSeq+i*N，
// 偏移 = 顺序扫描的记录起点），删掉后可逐字节一致地重建。
// 文件布局（小端）：magic "OSIX" + version u32 + N u32 + 锚点数 u32 +
// 锚点数组 (seq u64, offset u64)。
package sparse

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"

	"ontology/segment"
)

const (
	magic    = "OSIX"
	version  = 1
	headSize = 16
	anchorSz = 16
)

// Anchor 是一个稀疏锚点。
type Anchor struct {
	Seq    uint64
	Offset uint64
}

// Index 是一个段的稀疏索引。
type Index struct {
	N       int
	Anchors []Anchor
}

// Builder 在追加时增量收集锚点。
type Builder struct {
	n     int
	count int
	idx   Index
}

// NewBuilder 创建间隔为 n 的增量构建器。
func NewBuilder(n int) *Builder {
	return &Builder{n: n, idx: Index{N: n}}
}

// Observe 通知追加了一条事件（seq, offset）；每第 n 条记一个锚点。
func (b *Builder) Observe(seq, offset uint64) {
	if b.count%b.n == 0 {
		b.idx.Anchors = append(b.idx.Anchors, Anchor{Seq: seq, Offset: offset})
	}
	b.count++
}

// Index 返回已收集的索引。
func (b *Builder) Index() *Index { return &b.idx }

// Locate 返回「不大于 from 的最大锚点」；from 小于首锚点时 ok=false，
// 调用方应从段头扫起。不能用「最近锚点」：它可能落在 from 之后导致漏数据。
func (idx *Index) Locate(from uint64) (Anchor, bool) {
	i := sort.Search(len(idx.Anchors), func(i int) bool {
		return idx.Anchors[i].Seq > from
	})
	if i == 0 {
		return Anchor{}, false
	}
	return idx.Anchors[i-1], true
}

// Save 把索引编码写入 path（编码确定性，保证重建逐字节一致）。
func (idx *Index) Save(path string) error {
	return os.WriteFile(path, idx.encode(), 0o644)
}

func (idx *Index) encode() []byte {
	buf := make([]byte, headSize+len(idx.Anchors)*anchorSz)
	copy(buf, magic)
	binary.LittleEndian.PutUint32(buf[4:], version)
	binary.LittleEndian.PutUint32(buf[8:], uint32(idx.N))
	binary.LittleEndian.PutUint32(buf[12:], uint32(len(idx.Anchors)))
	for i, a := range idx.Anchors {
		off := headSize + i*anchorSz
		binary.LittleEndian.PutUint64(buf[off:], a.Seq)
		binary.LittleEndian.PutUint64(buf[off+8:], a.Offset)
	}
	return buf
}

// Load 读取索引文件。
func Load(path string) (*Index, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(buf) < headSize || string(buf[:4]) != magic {
		return nil, fmt.Errorf("sparse: bad index header")
	}
	n := int(binary.LittleEndian.Uint32(buf[8:]))
	cnt := int(binary.LittleEndian.Uint32(buf[12:]))
	if len(buf) != headSize+cnt*anchorSz {
		return nil, fmt.Errorf("sparse: index size %d, want %d", len(buf), headSize+cnt*anchorSz)
	}
	idx := &Index{N: n, Anchors: make([]Anchor, cnt)}
	for i := range idx.Anchors {
		off := headSize + i*anchorSz
		idx.Anchors[i] = Anchor{
			Seq:    binary.LittleEndian.Uint64(buf[off:]),
			Offset: binary.LittleEndian.Uint64(buf[off+8:]),
		}
	}
	return idx, nil
}

// Rebuild 只凭段文件内容重建索引（等价于追加期增量构建的结果）。
func Rebuild(segPath string, n int) (*Index, error) {
	f, err := os.Open(segPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	h, err := segment.ReadHeader(f)
	if err != nil {
		return nil, err
	}
	b := NewBuilder(n)
	err = segment.Scan(f, fi.Size(), h.FirstSeq, segment.HeaderSize, func(seq, offset uint64, _ []byte) {
		b.Observe(seq, offset)
	})
	if err != nil {
		return nil, err
	}
	return b.Index(), nil
}
