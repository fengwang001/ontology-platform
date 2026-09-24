// Package resume encodes and decodes BFS resume tokens.
//
// A token captures the full frontier state of a budgeted traversal:
// the done flag, the front frame's out-edge cursor, the FIFO queue of
// node IDs, and the sorted seen set. Layout:
//
//	magic(2) | flags(1) | Q(4) | S(4) | frontCursor(4) | crc32(4)
//	then Q queue entries, then S seen entries, each: idLen(1) | id bytes
//
// The CRC32 covers only the 15 header bytes (counts and cursor included);
// the ID payload is validated structurally and against the graph, so that
// corruption is classified into exactly three errors distinguishable with
// errors.Is: ErrChecksum, ErrIncomplete, ErrUnknownNode.
package resume

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"slices"

	"ontology/graph"
)

var (
	// ErrChecksum reports header integrity failure (magic, flags,
	// counts, cursor or CRC bytes corrupted).
	ErrChecksum = errors.New("resume: checksum failed")
	// ErrIncomplete reports truncated, overrun or internally
	// inconsistent structure.
	ErrIncomplete = errors.New("resume: incomplete or inconsistent fields")
	// ErrUnknownNode reports a well-formed token referencing a node
	// that does not exist in the graph.
	ErrUnknownNode = errors.New("resume: token references unknown node")
)

const (
	headerLen = 19
	crcLen    = 15 // header bytes covered by the checksum
	magic0    = 'R'
	magic1    = '1'
	flagDone  = 1
)

// State is the decoded content of a resume token.
type State struct {
	Done        bool     // traversal finished; resuming yields an empty sequence
	FrontCursor int      // next out-edge index of Queue[0]; 0 when Queue is empty
	Queue       []string // FIFO frontier, oldest first
	Seen        []string // visited node IDs, sorted lexicographically
}

// Encode serializes s into a token. Seen is sorted defensively so the
// encoding is deterministic regardless of the caller's ordering.
func Encode(s State) []byte {
	seen := slices.Clone(s.Seen)
	slices.Sort(seen)
	n := headerLen
	for _, id := range s.Queue {
		n += 1 + len(id)
	}
	for _, id := range seen {
		n += 1 + len(id)
	}
	buf := make([]byte, n)
	buf[0] = magic0
	buf[1] = magic1
	if s.Done {
		buf[2] = flagDone
	}
	binary.BigEndian.PutUint32(buf[3:7], uint32(len(s.Queue)))
	binary.BigEndian.PutUint32(buf[7:11], uint32(len(seen)))
	binary.BigEndian.PutUint32(buf[11:15], uint32(s.FrontCursor))
	binary.BigEndian.PutUint32(buf[15:19], crc32.ChecksumIEEE(buf[:crcLen]))
	off := headerLen
	for _, id := range s.Queue {
		off += putID(buf[off:], id)
	}
	for _, id := range seen {
		off += putID(buf[off:], id)
	}
	return buf
}

func putID(dst []byte, id string) int {
	dst[0] = byte(len(id))
	copy(dst[1:], id)
	return 1 + len(id)
}

// Decode parses and validates a token against g. Every node referenced by
// the token must exist in g, so deleting a queued node between two segments
// is reported as ErrUnknownNode naming that node.
func Decode(g *graph.Graph, data []byte) (State, error) {
	var st State
	if len(data) < headerLen {
		return st, fmt.Errorf("%w: token too short (%d bytes)", ErrIncomplete, len(data))
	}
	if data[0] != magic0 || data[1] != magic1 {
		return st, fmt.Errorf("%w: bad magic", ErrChecksum)
	}
	want := binary.BigEndian.Uint32(data[15:19])
	if got := crc32.ChecksumIEEE(data[:crcLen]); got != want {
		return st, fmt.Errorf("%w: header crc mismatch", ErrChecksum)
	}
	st.Done = data[2]&flagDone != 0
	qlen := int(binary.BigEndian.Uint32(data[3:7]))
	slen := int(binary.BigEndian.Uint32(data[7:11]))
	st.FrontCursor = int(binary.BigEndian.Uint32(data[11:15]))

	ids := make([]string, 0, qlen+slen)
	off := headerLen
	for i := 0; i < qlen+slen; i++ {
		if off >= len(data) {
			return st, fmt.Errorf("%w: truncated entry %d", ErrIncomplete, i)
		}
		l := int(data[off])
		off++
		if l == 0 || off+l > len(data) {
			return st, fmt.Errorf("%w: bad id length at entry %d", ErrIncomplete, i)
		}
		ids = append(ids, string(data[off:off+l]))
		off += l
	}
	if off != len(data) {
		return st, fmt.Errorf("%w: %d trailing bytes", ErrIncomplete, len(data)-off)
	}
	st.Queue, st.Seen = ids[:qlen], ids[qlen:]
	if err := st.checkShape(g); err != nil {
		return State{}, err
	}
	return st, nil
}

func (st State) checkShape(g *graph.Graph) error {
	if len(st.Queue) == 0 && !st.Done {
		return fmt.Errorf("%w: empty queue but not done", ErrIncomplete)
	}
	if len(st.Queue) > 0 && st.Done {
		return fmt.Errorf("%w: done but queue not empty", ErrIncomplete)
	}
	if len(st.Queue) == 0 && st.FrontCursor != 0 {
		return fmt.Errorf("%w: cursor without front frame", ErrIncomplete)
	}
	if !slices.IsSorted(st.Seen) {
		return fmt.Errorf("%w: seen set not sorted", ErrIncomplete)
	}
	inQueue := make(map[string]bool, len(st.Queue))
	for _, id := range st.Queue {
		if inQueue[id] {
			return fmt.Errorf("%w: duplicate queue entry %q", ErrIncomplete, id)
		}
		inQueue[id] = true
	}
	inSeen := make(map[string]bool, len(st.Seen))
	for i, id := range st.Seen {
		if i > 0 && id == st.Seen[i-1] {
			return fmt.Errorf("%w: duplicate seen entry %q", ErrIncomplete, id)
		}
		inSeen[id] = true
	}
	for _, id := range st.Queue[min(1, len(st.Queue)):] { // all but the front frame are already visited
		if !inSeen[id] {
			return fmt.Errorf("%w: queued node %q missing from seen set", ErrIncomplete, id)
		}
	}
	for _, id := range st.Queue {
		if !g.Has(id) {
			return fmt.Errorf("%w: %q", ErrUnknownNode, id)
		}
	}
	for _, id := range st.Seen {
		if !g.Has(id) {
			return fmt.Errorf("%w: %q", ErrUnknownNode, id)
		}
	}
	if len(st.Queue) > 0 && st.FrontCursor > g.OutDegree(st.Queue[0]) {
		return fmt.Errorf("%w: cursor %d past out-degree of %q", ErrIncomplete, st.FrontCursor, st.Queue[0])
	}
	return nil
}
