// Package dump 把剖析结果序列化为自描述文件并支持读回：
// 固定头 + 前序逐节点记录 + CRC32；截断文件时可恢复最大完整前缀。
package dump

import (
	"encoding/binary"
	"errors"
	"hash/crc32"

	"ontology/tree"
)

const (
	magic         = "ONTPROF1"
	version       = 1
	headerLen     = 36
	crcLen        = 4
	flagTruncated = 1
	recFixed      = 20 // self u64 + total u64 + depth u16 + flags u16
)

// 三类截断错误，errors.Is 可区分。
var (
	ErrHeader = errors.New("dump: header incomplete or invalid")
	ErrRecord = errors.New("dump: node record incomplete")
	ErrCRC    = errors.New("dump: crc mismatch")
)

// Profile 是一次剖析的可落盘结果。
type Profile struct {
	Tree    *tree.Tree
	Dropped uint64
}

func putU64(b []byte, v uint64) { binary.LittleEndian.PutUint64(b, v) }
func putU16(b []byte, v uint16) { binary.LittleEndian.PutUint16(b, v) }

// Encode 编码 Profile 为完整文件字节。
func Encode(p *Profile) []byte {
	var recs [][]byte
	var count uint64
	p.Tree.Walk(func(n *tree.Node, depth int) {
		if depth == 0 {
			return
		}
		count++
		name := []byte(n.Frame)
		rec := make([]byte, 2+len(name)+recFixed)
		putU16(rec, uint16(len(name)))
		copy(rec[2:], name)
		o := 2 + len(name)
		putU64(rec[o:], n.Self)
		putU64(rec[o+8:], n.Total)
		putU16(rec[o+16:], uint16(depth))
		var flags uint16
		if n.Truncated {
			flags = flagTruncated
		}
		putU16(rec[o+18:], flags)
		recs = append(recs, rec)
	})
	buf := make([]byte, headerLen)
	copy(buf, magic)
	putU16(buf[8:], version)
	putU16(buf[10:], headerLen)
	putU64(buf[12:], count)
	putU64(buf[20:], p.Tree.Samples)
	putU64(buf[28:], p.Dropped)
	for _, r := range recs {
		buf = append(buf, r...)
	}
	crc := crc32.ChecksumIEEE(buf)
	var tail [crcLen]byte
	putU32(tail[:], crc)
	return append(buf, tail[:]...)
}

func putU32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }

func parseHeader(b []byte) (nodeCount uint64, dropped uint64, err error) {
	if len(b) < headerLen || string(b[:len(magic)]) != magic ||
		binary.LittleEndian.Uint16(b[8:]) != version ||
		binary.LittleEndian.Uint16(b[10:]) != headerLen {
		return 0, 0, ErrHeader
	}
	return binary.LittleEndian.Uint64(b[12:]), binary.LittleEndian.Uint64(b[28:]), nil
}

type recInfo struct {
	frame     string
	self      uint64
	total     uint64
	depth     int
	truncated bool
}

func parseRecords(b []byte, want uint64) ([]recInfo, int, error) {
	out := []recInfo{}
	off := 0
	for i := uint64(0); i < want; i++ {
		if off+2 > len(b) {
			return out, off, ErrRecord
		}
		nl := int(binary.LittleEndian.Uint16(b[off:]))
		end := off + 2 + nl + recFixed
		if end > len(b) {
			return out, off, ErrRecord
		}
		ri := recInfo{frame: string(b[off+2 : off+2+nl]), depth: int(binary.LittleEndian.Uint16(b[off+2+nl+16:]))}
		ri.self = binary.LittleEndian.Uint64(b[off+2+nl:])
		ri.total = binary.LittleEndian.Uint64(b[off+2+nl+8:])
		ri.truncated = binary.LittleEndian.Uint16(b[off+2+nl+18:])&flagTruncated != 0
		out = append(out, ri)
		off = end
	}
	return out, off, nil
}

// Decode 严格读回完整文件；任何截断/损坏返回可 errors.Is 判别的错误。
func Decode(b []byte) (*Profile, error) {
	want, dropped, err := parseHeader(b)
	if err != nil {
		return nil, err
	}
	body := b[headerLen:]
	recs, off, err := parseRecords(body, want)
	if err != nil {
		return nil, err
	}
	if len(body) < off+crcLen {
		return nil, ErrCRC
	}
	got := binary.LittleEndian.Uint32(body[off : off+crcLen])
	if crc32.ChecksumIEEE(b[:headerLen+off]) != got {
		return nil, ErrCRC
	}
	tr := buildTree(recs)
	return &Profile{Tree: tr, Dropped: dropped}, nil
}

// Recover 从可能被截断的字节中读出「最大可恢复前缀」，同时返回分类错误。
// 返回的 Profile 永不为 nil；按前序重建保证不会出现父缺子在。
func Recover(b []byte) (*Profile, error) {
	want, _, err := parseHeader(b)
	if err != nil {
		return &Profile{Tree: tree.New()}, err
	}
	recs, _, err := parseRecords(b[headerLen:], want)
	tr := buildTree(recs)
	return &Profile{Tree: tr}, err // err 为 ErrRecord 或 nil
}

func buildTree(recs []recInfo) *tree.Tree {
	tr := tree.New()
	stack := []*tree.Node{tr.Root}
	var selfSum uint64
	for _, ri := range recs {
		for len(stack) > ri.depth {
			stack = stack[:len(stack)-1]
		}
		parent := stack[len(stack)-1]
		n := &tree.Node{Frame: ri.frame, Self: ri.self, Total: ri.total, Truncated: ri.truncated}
		parent.Children = append(parent.Children, n)
		stack = append(stack, n)
		selfSum += ri.self
	}
	tr.Samples = selfSum // 恢复树自洽：Samples 定义为已恢复节点 self 之和
	return tr
}
