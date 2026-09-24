// Package dump 把剖析结果落盘并读回：自描述头 + 先序节点记录 + CRC32。
package dump

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"

	"ontology/tree"
)

// 三类截断/损坏错误，可用 errors.Is 区分。
var (
	ErrHeaderIncomplete = errors.New("dump: header incomplete")
	ErrRecordIncomplete = errors.New("dump: node record incomplete")
	ErrCRCMismatch      = errors.New("dump: crc32 mismatch")
)

const (
	magic      = 0x4F4E5444 // "ONTD"
	version    = 1
	HeaderSize = 28
	fixedSize  = 23 // 记录定长部分：parent i32 + self i64 + total i64 + flags u8 + frameLen u16
	flagTrunc  = 1
)

type record struct {
	parent int32
	frame  string
	self   int64
	total  int64
	trunc  bool
}

// Marshal 序列化整棵树：先序记录保证父节点下标恒小于子节点。
func Marshal(t *tree.Tree) []byte {
	root := t.Snapshot()
	var recs []record
	var walk func(n *tree.Node, parent int32)
	walk = func(n *tree.Node, parent int32) {
		idx := int32(len(recs))
		recs = append(recs, record{parent: parent, frame: n.Frame, self: n.Self, total: n.Total, trunc: n.Truncated})
		for _, c := range n.Children {
			walk(c, idx)
		}
	}
	walk(root, -1)

	buf := make([]byte, 0, HeaderSize+len(recs)*fixedSize+4)
	buf = binary.BigEndian.AppendUint32(buf, magic)
	buf = binary.BigEndian.AppendUint16(buf, version)
	buf = binary.BigEndian.AppendUint16(buf, 0)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(recs)))
	buf = binary.BigEndian.AppendUint64(buf, uint64(t.Samples))
	buf = binary.BigEndian.AppendUint64(buf, uint64(t.TruncatedSamples))
	for _, r := range recs {
		buf = binary.BigEndian.AppendUint32(buf, uint32(r.parent))
		buf = binary.BigEndian.AppendUint64(buf, uint64(r.self))
		buf = binary.BigEndian.AppendUint64(buf, uint64(r.total))
		var flags byte
		if r.trunc {
			flags = flagTrunc
		}
		buf = append(buf, flags)
		buf = binary.BigEndian.AppendUint16(buf, uint16(len(r.frame)))
		buf = append(buf, r.frame...)
	}
	return binary.BigEndian.AppendUint32(buf, crc32.ChecksumIEEE(buf))
}

// Unmarshal 严格读回完整文件，任何一类错误都直接失败。
func Unmarshal(data []byte) (*tree.Tree, error) {
	tr, _, err := Recover(data)
	if err != nil {
		return nil, err
	}
	return tr, nil
}

// Recover 解析 data 的最长完整前缀，返回恢复出的树、消耗的字节数与分类错误。
// 完整文件返回 nil 错误；截断文件返回三类错误之一与已恢复的前缀树。
func Recover(data []byte) (*tree.Tree, int, error) {
	tr := tree.New()
	if len(data) < HeaderSize {
		return tr, 0, ErrHeaderIncomplete
	}
	if binary.BigEndian.Uint32(data) != magic || binary.BigEndian.Uint16(data[4:]) != version {
		return nil, 0, fmt.Errorf("dump: bad magic or version")
	}
	nodeCount := binary.BigEndian.Uint32(data[8:])
	off := HeaderSize
	nodes := []*tree.Node{tr.Root}
	var parsed uint32
	var selfSum, truncCount int64
	for parsed < nodeCount {
		if len(data)-off < fixedSize {
			return finish(tr, selfSum, truncCount, off, ErrRecordIncomplete)
		}
		parent := int32(binary.BigEndian.Uint32(data[off:]))
		self := int64(binary.BigEndian.Uint64(data[off+4:]))
		total := int64(binary.BigEndian.Uint64(data[off+12:]))
		trunc := data[off+20]&flagTrunc != 0
		frameLen := int(binary.BigEndian.Uint16(data[off+21:]))
		if len(data)-off-fixedSize < frameLen {
			return finish(tr, selfSum, truncCount, off, ErrRecordIncomplete)
		}
		frame := string(data[off+fixedSize : off+fixedSize+frameLen])
		off += fixedSize + frameLen
		if parsed == 0 {
			if parent != -1 || frame != "" {
				return finish(tr, selfSum, truncCount, off, ErrRecordIncomplete)
			}
			tr.Root.Self, tr.Root.Total, tr.Root.Truncated = self, total, trunc
		} else {
			if parent < 0 || int(parent) >= len(nodes) {
				return finish(tr, selfSum, truncCount, off, ErrRecordIncomplete)
			}
			nodes = append(nodes, tr.Attach(nodes[parent], frame, self, total, trunc))
		}
		selfSum += self
		if trunc {
			truncCount++
		}
		parsed++
	}
	if len(data)-off < 4 || binary.BigEndian.Uint32(data[off:]) != crc32.ChecksumIEEE(data[:off]) {
		return finish(tr, selfSum, truncCount, off, ErrCRCMismatch)
	}
	return finish(tr, selfSum, truncCount, off+4, nil)
}

func finish(tr *tree.Tree, samples, truncated int64, consumed int, err error) (*tree.Tree, int, error) {
	tr.Samples = samples
	tr.TruncatedSamples = truncated
	return tr, consumed, err
}
