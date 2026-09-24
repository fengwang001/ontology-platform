// Package dump persists profile snapshots and recovers truncated files.
//
// Layout: 32-byte self-describing header, preorder node records
// (27-byte fixed part plus frame name), and a CRC32-IEEE trailer
// covering all preceding bytes.
package dump

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	"ontology/tree"
)

// Truncation classes, distinguishable with errors.Is.
var (
	ErrHeaderIncomplete = errors.New("dump: header incomplete")
	ErrRecordIncomplete = errors.New("dump: node record incomplete")
	ErrCRCMismatch      = errors.New("dump: crc mismatch")
)

var magic = [8]byte{'O', 'N', 'T', 'O', 'P', 'R', 'F', '1'}

const (
	headerSize = 32
	recordFix  = 27
	flagTrunc  = 1
	flagRoot   = 2
)

// Write serializes snap: header, preorder records, CRC32 trailer.
func Write(w io.Writer, snap *tree.Snapshot) error {
	var nodes []*tree.SNode
	parents := make(map[*tree.SNode]int32)
	var walk func(n *tree.SNode)
	walk = func(n *tree.SNode) {
		parents[n] = int32(len(nodes))
		nodes = append(nodes, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(snap.Root)

	buf := new(bytes.Buffer)
	hdr := make([]byte, headerSize)
	copy(hdr, magic[:])
	binary.BigEndian.PutUint16(hdr[8:], 1)
	binary.BigEndian.PutUint64(hdr[12:], uint64(snap.Samples))
	binary.BigEndian.PutUint64(hdr[20:], uint64(snap.TruncatedSamples))
	binary.BigEndian.PutUint32(hdr[28:], uint32(len(nodes)))
	buf.Write(hdr)
	for i, n := range nodes {
		rec := make([]byte, recordFix+len(n.Frame))
		parent := int32(-1)
		if n.Parent != nil {
			parent = parents[n.Parent]
		}
		binary.BigEndian.PutUint32(rec[0:], uint32(parent))
		binary.BigEndian.PutUint64(rec[4:], uint64(n.Self))
		binary.BigEndian.PutUint64(rec[12:], uint64(n.Total))
		var flags byte
		if n.Truncated {
			flags |= flagTrunc
		}
		if i == 0 {
			flags |= flagRoot
		}
		rec[24] = flags
		binary.BigEndian.PutUint16(rec[25:], uint16(len(n.Frame)))
		copy(rec[27:], n.Frame)
		buf.Write(rec)
	}
	crc := crc32.ChecksumIEEE(buf.Bytes())
	var trailer [4]byte
	binary.BigEndian.PutUint32(trailer[:], crc)
	buf.Write(trailer[:])
	_, err := w.Write(buf.Bytes())
	return err
}

// Read strictly decodes a full file; any corruption is an error.
func Read(data []byte) (*tree.Snapshot, error) {
	snap, err := Recover(data)
	if err != nil {
		return nil, err
	}
	return snap, nil
}

// Recover decodes the maximal recoverable prefix of data. It returns a
// self-consistent snapshot (no orphans: preorder keeps parents first)
// whose Samples is the self-sum of the recovered nodes, plus the
// classified truncation error (nil when the file is intact).
func Recover(data []byte) (*tree.Snapshot, error) {
	if len(data) < headerSize {
		return &tree.Snapshot{Root: &tree.SNode{}}, ErrHeaderIncomplete
	}
	nodeCount := int(binary.BigEndian.Uint32(data[28:]))
	truncated := int64(binary.BigEndian.Uint64(data[20:]))
	nodes := make([]*tree.SNode, 0, nodeCount)
	var root *tree.SNode
	off := headerSize
	for len(nodes) < nodeCount {
		if len(data)-off < recordFix {
			return finish(nodes, root, truncated), ErrRecordIncomplete
		}
		rec := data[off:]
		parent := int32(binary.BigEndian.Uint32(rec[0:]))
		nameLen := int(binary.BigEndian.Uint16(rec[25:]))
		if len(data)-off < recordFix+nameLen {
			return finish(nodes, root, truncated), ErrRecordIncomplete
		}
		n := &tree.SNode{
			Self:      int64(binary.BigEndian.Uint64(rec[4:])),
			Total:     int64(binary.BigEndian.Uint64(rec[12:])),
			Truncated: rec[24]&flagTrunc != 0,
			Frame:     string(rec[27 : 27+nameLen]),
		}
		if len(nodes) == 0 && rec[24]&flagRoot != 0 {
			root = n
		} else if parent >= 0 && int(parent) < len(nodes) {
			n.Parent = nodes[parent]
			n.Parent.Children = append(n.Parent.Children, n)
		}
		nodes = append(nodes, n)
		off += recordFix + nameLen
	}
	if len(data)-off < 4 {
		return finish(nodes, root, truncated), ErrCRCMismatch
	}
	want := binary.BigEndian.Uint32(data[off:])
	if crc32.ChecksumIEEE(data[:off]) != want {
		return finish(nodes, root, truncated), ErrCRCMismatch
	}
	return finish(nodes, root, truncated), nil
}

// finish links the recovered forest under a synthetic root and sets
// Samples to the self-sum of recovered nodes (self-consistency).
func finish(nodes []*tree.SNode, root *tree.SNode, truncated int64) *tree.Snapshot {
	if root == nil {
		root = &tree.SNode{}
	}
	var selfSum int64
	for _, n := range nodes {
		selfSum += n.Self
		if n.Parent == nil && n != root {
			n.Parent = root
			root.Children = append(root.Children, n)
		}
	}
	return &tree.Snapshot{Root: root, Samples: selfSum, TruncatedSamples: truncated}
}
