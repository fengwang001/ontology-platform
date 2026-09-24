// Package sparse 实现稀疏索引：每 N 条事件记录一个「序号 → 字节偏移」锚点。
// 索引是纯加速结构，全部内容可由段文件重建（见 DESIGN.md）。
package sparse

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

// 索引文件布局：magic(4) + every(4) + 锚点数(4) + 锚点序列 (seq(8), offset(8))。
const Magic = "OSIX"

// ErrBadIndex 表示索引文件本身无法解析。
var ErrBadIndex = errors.New("sparse: malformed index file")

// Anchor 把一个事件序号映射到它在段文件中的字节偏移。
type Anchor struct {
	Seq    uint64
	Offset int64
}

// Index 是一个段文件的稀疏索引。
type Index struct {
	Every   int
	Anchors []Anchor
}

// Locate 返回「不大于 from 的最大锚点」；所有锚点都大于 from 时返回 false。
// 不能取「最接近 from 的锚点」：它可能落在 from 之后，向前扫描会漏事件（见 DESIGN.md）。
func (idx Index) Locate(from uint64) (Anchor, bool) {
	i := sort.Search(len(idx.Anchors), func(i int) bool { return idx.Anchors[i].Seq > from }) - 1
	if i < 0 {
		return Anchor{}, false
	}
	return idx.Anchors[i], true
}

// Encode 把索引序列化为确定性的字节串（同样内容必然同样字节）。
func Encode(idx Index) []byte {
	buf := make([]byte, 12+16*len(idx.Anchors))
	copy(buf, Magic)
	binary.BigEndian.PutUint32(buf[4:], uint32(idx.Every))
	binary.BigEndian.PutUint32(buf[8:], uint32(len(idx.Anchors)))
	for i, a := range idx.Anchors {
		binary.BigEndian.PutUint64(buf[12+16*i:], a.Seq)
		binary.BigEndian.PutUint64(buf[12+16*i+8:], uint64(a.Offset))
	}
	return buf
}

// Decode 解析索引文件内容。
func Decode(buf []byte) (Index, error) {
	if len(buf) < 12 || string(buf[:4]) != Magic {
		return Index{}, ErrBadIndex
	}
	every := binary.BigEndian.Uint32(buf[4:])
	n := binary.BigEndian.Uint32(buf[8:])
	if len(buf) != 12+16*int(n) {
		return Index{}, fmt.Errorf("%w: size %d", ErrBadIndex, len(buf))
	}
	idx := Index{Every: int(every), Anchors: make([]Anchor, n)}
	for i := range idx.Anchors {
		idx.Anchors[i] = Anchor{
			Seq:    binary.BigEndian.Uint64(buf[12+16*i:]),
			Offset: int64(binary.BigEndian.Uint64(buf[12+16*i+8:])),
		}
	}
	return idx, nil
}

// WriteFile 把索引写入 path。
func WriteFile(path string, idx Index) error {
	return os.WriteFile(path, Encode(idx), 0o644)
}

// ReadFile 从 path 读取索引。
func ReadFile(path string) (Index, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return Index{}, err
	}
	return Decode(buf)
}
