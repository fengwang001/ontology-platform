// Package segment 实现自描述段文件的写入与读取。
// 布局：magic(8) | dataLen(4 LE) | dictLen(varint) | dict | posts | crc32(4)
// dict 每条：termLen(varint) term postOff(varint) postLen(varint) chainCRC32(4)
// posts 为各词编码链顺序拼接；末尾 CRC32-IEEE 覆盖此前全部字节。
package segment

import (
	"encoding/binary"
	"hash/crc32"
	"sort"

	"ontology/posting"
)

// Magic 是段文件魔数。
const Magic = "ONSEG001"

var crcTable = crc32.MakeTable(crc32.IEEE)

type entry struct {
	term             string
	postOff, postLen uint64
	chainCRC         uint32
	data             []byte
}

// Writer 收集链并序列化为一个段。
type Writer struct {
	entries []*entry
}

// NewWriter 创建空段写入器。
func NewWriter() *Writer { return &Writer{} }

// AddChain 追加一条倒排链（写入时按词元排序）。
func (w *Writer) AddChain(c *posting.Chain) {
	data := posting.Encode(c)
	w.entries = append(w.entries, &entry{
		term:     c.Term,
		postLen:  uint64(len(data)),
		chainCRC: crc32.Checksum(data, crcTable),
		data:     data,
	})
}

// Serialize 生成完整段字节（含 CRC）。
func (w *Writer) Serialize() []byte {
	sort.Slice(w.entries, func(i, j int) bool { return w.entries[i].term < w.entries[j].term })
	var posts []byte
	for _, e := range w.entries {
		e.postOff = uint64(len(posts))
		posts = append(posts, e.data...)
	}
	var dict []byte
	dict = posting.AppendUvarint(dict, uint64(len(w.entries)))
	for _, e := range w.entries {
		dict = posting.AppendUvarint(dict, uint64(len(e.term)))
		dict = append(dict, e.term...)
		dict = posting.AppendUvarint(dict, e.postOff)
		dict = posting.AppendUvarint(dict, e.postLen)
		dict = binary.LittleEndian.AppendUint32(dict, e.chainCRC)
	}
	var head []byte
	head = append(head, Magic...)
	data := append([]byte{}, dict...)
	data = append(data, posts...)
	head = binary.LittleEndian.AppendUint32(head, uint32(1+len(dict)+len(posts)))
	dl := posting.AppendUvarint(nil, uint64(len(dict)))
	out := append(head, dl...)
	out = append(out, data...)
	sum := crc32.Checksum(out, crcTable)
	return binary.LittleEndian.AppendUint32(out, sum)
}

// Entry 是词典项的公开视图。
type Entry struct {
	Term             string
	PostOff, PostLen uint64
	ChainCRC         uint32
}
