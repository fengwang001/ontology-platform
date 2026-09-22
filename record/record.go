// Package record 定义记录及其二进制编解码。
package record

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Record 是一条待排序记录：字符串键、字节串值、全局到达序号。
type Record struct {
	Key   string
	Value []byte
	Seq   uint64
}

// headerSize 是编码后固定头长度：keyLen u32 + valLen u32 + seq u64。
const headerSize = 4 + 4 + 8

// ErrMalformed 表示编码字节串不合法。
var ErrMalformed = errors.New("record: malformed encoding")

// Size 返回记录的编码字节数，也是内存记账的单位大小。
func (r Record) Size() int {
	return headerSize + len(r.Key) + len(r.Value)
}

// Marshal 把记录编码为 []byte。
func (r Record) Marshal() []byte {
	buf := make([]byte, r.Size())
	binary.LittleEndian.PutUint32(buf[0:4], uint32(len(r.Key)))
	binary.LittleEndian.PutUint32(buf[4:8], uint32(len(r.Value)))
	binary.LittleEndian.PutUint64(buf[8:16], r.Seq)
	copy(buf[16:], r.Key)
	copy(buf[16+len(r.Key):], r.Value)
	return buf
}

// Unmarshal 从 []byte 解码记录，输入必须恰好是一条完整记录的编码。
func Unmarshal(buf []byte) (Record, error) {
	var r Record
	if len(buf) < headerSize {
		return r, fmt.Errorf("%w: too short: %d", ErrMalformed, len(buf))
	}
	keyLen := int(binary.LittleEndian.Uint32(buf[0:4]))
	valLen := int(binary.LittleEndian.Uint32(buf[4:8]))
	if keyLen < 0 || valLen < 0 || len(buf) != headerSize+keyLen+valLen {
		return r, fmt.Errorf("%w: bad lengths key=%d val=%d total=%d",
			ErrMalformed, keyLen, valLen, len(buf))
	}
	r.Seq = binary.LittleEndian.Uint64(buf[8:16])
	r.Key = string(buf[16 : 16+keyLen])
	r.Value = append([]byte(nil), buf[16+keyLen:]...)
	return r, nil
}

// Less 是全局全序比较键：先按 Key 字典序，键相同按到达序号 Seq。
// Seq 在 Ingest 时全局唯一分配，故 (Key, Seq) 构成全序，等键记录按到达顺序排列。
func Less(a, b Record) bool {
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	return a.Seq < b.Seq
}
