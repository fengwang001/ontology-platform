package logstore

import (
	"fmt"
	"sort"
	"strings"
)

// BlockInfo describes one on-disk block for inspection.
type BlockInfo struct {
	Key       string
	Tombstone bool
	TS        uint64
	Size      int
	Live      bool
}

// SegmentInfo describes one occupied segment slot for inspection.
type SegmentInfo struct {
	ID        int
	Used      int
	LiveBytes int
	Sealed    bool
	MaxTS     uint64
	Blocks    []BlockInfo
}

// Segments returns a snapshot of all occupied slots, ordered by slot id.
func (s *Store) Segments() []SegmentInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []SegmentInfo
	for _, seg := range s.slots {
		if seg == nil {
			continue
		}
		info := SegmentInfo{
			ID:        seg.id,
			Used:      seg.used,
			LiveBytes: s.liveBytes(seg),
			Sealed:    seg.sealed,
			MaxTS:     seg.maxTS,
		}
		for _, b := range seg.blocks {
			info.Blocks = append(info.Blocks, BlockInfo{
				Key:       b.key,
				Tombstone: b.tombstone,
				TS:        b.ts,
				Size:      b.size,
				Live:      s.blockLive(b),
			})
		}
		out = append(out, info)
	}
	return out
}

// FreeSegments reports the number of unoccupied slots.
func (s *Store) FreeSegments() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.freeSlots()
}

// VerifyAccounting checks the per-segment live-bytes invariant: for every
// occupied segment, live bytes equal the bytes of index-pointed data blocks
// residing in the segment plus the bytes of its live tombstones.
func (s *Store) VerifyAccounting() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, seg := range s.slots {
		if seg == nil {
			continue
		}
		live := s.liveBytes(seg)
		want := 0
		for _, b := range s.index {
			if !b.tombstone && b.seg == seg.id {
				want += b.size
			}
		}
		for _, b := range seg.blocks {
			if b.tombstone && s.blockLive(b) {
				want += b.size
			}
		}
		if live != want {
			return fmt.Errorf("logstore: segment %d live=%d want=%d", seg.id, live, want)
		}
	}
	return nil
}

// Dump returns a deterministic textual layout of the store: occupied slots
// in id order with their blocks, plus the index mapping. Identical operation
// sequences must produce identical dumps.
func (s *Store) Dump() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var sb strings.Builder
	fmt.Fprintf(&sb, "clock=%d current=%d free=%d\n", s.clock, s.current.id, s.freeSlots())
	for _, seg := range s.slots {
		if seg == nil {
			continue
		}
		fmt.Fprintf(&sb, "seg %d used=%d live=%d sealed=%v maxTS=%d\n",
			seg.id, seg.used, s.liveBytes(seg), seg.sealed, seg.maxTS)
		for _, b := range seg.blocks {
			fmt.Fprintf(&sb, "  block key=%q tomb=%v ts=%d size=%d live=%v\n",
				b.key, b.tombstone, b.ts, b.size, s.blockLive(b))
		}
	}
	keys := make([]string, 0, len(s.index))
	for k := range s.index {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b := s.index[k]
		fmt.Fprintf(&sb, "index %q -> seg=%d ts=%d tomb=%v\n", k, b.seg, b.ts, b.tombstone)
	}
	return sb.String()
}
