// Package posting 定义倒排链及其增量编码。
// 文档号在链内严格升序；每篇文档内位置严格升序；位置从 0 开始。
package posting

import (
	"encoding/binary"
	"errors"
	"io"
)

// Doc 是一篇文档在某词倒排链中的出现信息。
type Doc struct {
	ID        uint32
	Positions []uint32
}

// Chain 是一个词的完整倒排链。
type Chain struct {
	Term string
	Docs []Doc
}

// ErrDecode 在增量数据损坏时返回。
var ErrDecode = errors.New("posting: decode error")

// AppendUvarint 追加一个无符号变长整数。
func AppendUvarint(b []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	return append(b, buf[:n]...)
}

// ReadUvarint 从 b 的 off 处读取一个 uvarint，返回值与新偏移。
func ReadUvarint(b []byte, off int) (uint64, int, error) {
	v, n := binary.Uvarint(b[off:])
	if n <= 0 {
		return 0, 0, ErrDecode
	}
	return v, off + n, nil
}

// Encode 将倒排链编码为增量字节串。
// 布局：docNum；每文档 [docDelta][posNum][posDelta...]，
// 位置以该文档首个位置为基准做差。
func Encode(c *Chain) []byte {
	b := AppendUvarint(nil, uint64(len(c.Docs)))
	var prevDoc uint64
	for _, d := range c.Docs {
		b = AppendUvarint(b, uint64(d.ID)-prevDoc)
		prevDoc = uint64(d.ID)
		b = AppendUvarint(b, uint64(len(d.Positions)))
		var base uint64
		for i, p := range d.Positions {
			if i == 0 {
				base = uint64(p)
				b = AppendUvarint(b, base)
			} else {
				b = AppendUvarint(b, uint64(p)-base)
			}
		}
	}
	return b
}

// Decode 还原 Encode 的结果。
func Decode(b []byte) (*Chain, error) {
	off := 0
	docNum, o, err := ReadUvarint(b, off)
	if err != nil {
		return nil, err
	}
	off = o
	c := &Chain{}
	var prevDoc uint64
	for i := uint64(0); i < docNum; i++ {
		dd, o2, e := ReadUvarint(b, off)
		if e != nil {
			return nil, e
		}
		off = o2
		docID := prevDoc + dd
		prevDoc = docID
		pn, o3, e := ReadUvarint(b, off)
		if e != nil {
			return nil, e
		}
		off = o3
		doc := Doc{ID: uint32(docID), Positions: make([]uint32, 0, pn)}
		var base uint64
		for j := uint64(0); j < pn; j++ {
			pd, o4, e := ReadUvarint(b, off)
			if e != nil {
				return nil, e
			}
			off = o4
			if j == 0 {
				base = pd
			} else {
				pd += base
			}
			doc.Positions = append(doc.Positions, uint32(pd))
		}
		c.Docs = append(c.Docs, doc)
	}
	if off != len(b) {
		return nil, ErrDecode
	}
	return c, nil
}

// WriteChain 将一条编码链写入 w，返回写入字节数。
func WriteChain(w io.Writer, b []byte) (int, error) {
	return w.Write(b)
}
