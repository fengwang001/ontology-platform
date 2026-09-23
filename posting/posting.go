// Package posting 定义倒排链（文档号升序 + 每篇文档内位置升序）及其增量编码。
package posting

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// ErrDuplicate 表示同一 (doc,pos) 被重复添加。
var ErrDuplicate = errors.New("posting: duplicate (doc,pos)")

// ErrOutOfOrder 表示添加顺序违反 (doc,pos) 升序。
var ErrOutOfOrder = errors.New("posting: additions must be in (doc,pos) order")

// Posting 是一篇文档的倒排项：文档号 + 升序位置表。
type Posting struct {
	Doc uint32
	Pos []uint32
}

// List 是按文档号升序的倒排链。
type List []Posting

// Builder 以 (doc,pos) 升序构建倒排链，拒绝并计数重复添加。
type Builder struct {
	list    List
	lastDoc uint32
	lastPos uint32
	started bool
	dups    int
}

// Add 追加一个 (doc,pos)。重复添加返回 ErrDuplicate 并计数；
// 逆序添加返回 ErrOutOfOrder。
func (b *Builder) Add(doc, pos uint32) error {
	if b.started {
		if doc == b.lastDoc && pos == b.lastPos {
			b.dups++
			return ErrDuplicate
		}
		if doc < b.lastDoc || (doc == b.lastDoc && pos < b.lastPos) {
			return ErrOutOfOrder
		}
	}
	b.started = true
	if n := len(b.list); n > 0 && b.list[n-1].Doc == doc {
		b.list[n-1].Pos = append(b.list[n-1].Pos, pos)
	} else {
		b.list = append(b.list, Posting{Doc: doc, Pos: []uint32{pos}})
	}
	b.lastDoc, b.lastPos = doc, pos
	return nil
}

// Dups 返回被拒绝的重复添加次数。
func (b *Builder) Dups() int { return b.dups }

// List 返回构建出的倒排链。
func (b *Builder) List() List { return b.list }

// Encode 将倒排链编码为差分 varint 字节串。
func Encode(l List) []byte {
	var buf []byte
	var tmp [binary.MaxVarintLen64]byte
	put := func(v uint64) {
		n := binary.PutUvarint(tmp[:], v)
		buf = append(buf, tmp[:n]...)
	}
	put(uint64(len(l)))
	var prevDoc uint32
	for i, p := range l {
		if i == 0 {
			put(uint64(p.Doc))
		} else {
			put(uint64(p.Doc - prevDoc))
		}
		prevDoc = p.Doc
		put(uint64(len(p.Pos)))
		var prevPos uint32
		for j, pos := range p.Pos {
			if j == 0 {
				put(uint64(pos))
			} else {
				put(uint64(pos - prevPos))
			}
			prevPos = pos
		}
	}
	return buf
}

// Decode 解码 Encode 的产物，并校验升序不变量。
func Decode(data []byte) (List, error) {
	off := 0
	get := func() (uint64, error) {
		v, n := binary.Uvarint(data[off:])
		if n <= 0 {
			return 0, errors.New("posting: truncated varint")
		}
		off += n
		return v, nil
	}
	docCount, err := get()
	if err != nil {
		return nil, err
	}
	l := make(List, 0, docCount)
	var prevDoc uint64
	for d := uint64(0); d < docCount; d++ {
		delta, err := get()
		if err != nil {
			return nil, err
		}
		doc := delta
		if d > 0 {
			doc = prevDoc + delta
			if delta == 0 {
				return nil, fmt.Errorf("posting: doc ids not ascending at %d", d)
			}
		}
		prevDoc = doc
		posCount, err := get()
		if err != nil {
			return nil, err
		}
		pos := make([]uint32, 0, posCount)
		var prevPos uint64
		for j := uint64(0); j < posCount; j++ {
			pd, err := get()
			if err != nil {
				return nil, err
			}
			p := pd
			if j > 0 {
				p = prevPos + pd
				if pd == 0 {
					return nil, fmt.Errorf("posting: positions not ascending at doc %d", doc)
				}
			}
			prevPos = p
			pos = append(pos, uint32(p))
		}
		l = append(l, Posting{Doc: uint32(doc), Pos: pos})
	}
	if off != len(data) {
		return nil, errors.New("posting: trailing bytes")
	}
	return l, nil
}
