package logstore

import (
	"errors"
	"testing"
)

// TestTombstoneRetention walks through the full tombstone life cycle:
//
//  1. k1 is put in slot0, then deleted; the tombstone lands in slot1 while
//     the older data block still sits in slot0.
//  2. Cleaning slot1 must MIGRATE the tombstone (it is live: an older block
//     for k1 survives in another segment).
//  3. Reclaiming slot0 drops the last older block; the tombstone turns dead.
//  4. Cleaning the tombstone's new segment now DROPS the tombstone.
//
// SegmentSize=52 (two 26-byte data blocks per segment), tombstone size=18.
func TestTombstoneRetention(t *testing.T) {
	s := newStore(t, 52, 5)

	putBytes(t, s, "k1", 8) // ts1, slot0
	putBytes(t, s, "k2", 8) // ts2, slot0 full
	putBytes(t, s, "k3", 8) // ts3, slot1
	del(t, s, "k1")         // ts4, tombstone in slot1; older k1 block in slot0
	putBytes(t, s, "k3", 8) // ts5, overwrite k3 -> slot1 keeps only the tombstone live
	putBytes(t, s, "k4", 8) // ts6, slot2 full
	putBytes(t, s, "k5", 8) // ts7, slot3
	putBytes(t, s, "k6", 8) // ts8, slot3 full
	putBytes(t, s, "k7", 8) // ts9, slot4 (current, remaining 26)
	mustVerify(t, s)

	// Phase 1: clean. Victim must be slot1 (score 34*5/70 = 17/7 > 7/3 of
	// slot0). Its only live block is the tombstone, which must be migrated.
	if err := s.cleanOne(); err != nil {
		t.Fatalf("clean phase 1: %v", err)
	}
	mustVerify(t, s)
	seg, tomb, ok := findBlock(s, "k1", true)
	if !ok {
		t.Fatal("phase 1: tombstone must survive cleaning while older block exists")
	}
	t.Logf("phase 1 output: tombstone migrated to segment %d, live=%v (older k1 block still in slot0)",
		seg.ID, tomb.Live)
	if seg.ID != 4 || !tomb.Live || tomb.TS != 4 {
		t.Fatalf("phase 1: tombstone in seg=%d live=%v ts=%d, want seg=4 live=true ts=4",
			seg.ID, tomb.Live, tomb.TS)
	}
	if _, ok := s.Get("k1"); ok {
		t.Fatal("phase 1: k1 must read as deleted")
	}

	// Phase 2: reclaim slot0 (holds the older k1 block). k2 migrates out.
	putBytes(t, s, "k8", 8) // ts10, reuse freed slot1 as current
	if err := s.cleanOne(); err != nil {
		t.Fatalf("clean phase 2: %v", err)
	}
	mustVerify(t, s)
	seg, tomb, ok = findBlock(s, "k1", true)
	if !ok {
		t.Fatal("phase 2: tombstone block should still be on disk (not yet cleaned)")
	}
	t.Logf("phase 2 output: older k1 block reclaimed; tombstone in segment %d now live=%v",
		seg.ID, tomb.Live)
	if tomb.Live {
		t.Fatal("phase 2: tombstone must be dead once no older block remains")
	}

	// Phase 3: clean the segment holding the dead tombstone; it is dropped.
	putBytes(t, s, "k9", 8) // ts11, reuse freed slot0 as current
	if err := s.cleanOne(); err != nil {
		t.Fatalf("clean phase 3: %v", err)
	}
	mustVerify(t, s)
	if _, _, ok := findBlock(s, "k1", true); ok {
		t.Fatal("phase 3: dead tombstone must be dropped")
	}
	t.Log("phase 3 output: dead tombstone dropped; k1 fully gone")
	if _, ok := s.Get("k1"); ok {
		t.Fatal("phase 3: k1 must read as deleted")
	}
	t.Logf("final layout:\n%s", s.Dump())
}

// TestMigrationPreservesAge checks that migrated blocks keep their original
// write timestamps, so a segment's age (clock - maxTS) is not reset by
// cleaning.
func TestMigrationPreservesAge(t *testing.T) {
	s := newStore(t, 52, 3)
	putBytes(t, s, "k1", 8) // ts1, slot0
	putBytes(t, s, "k2", 8) // ts2, slot0 full
	putBytes(t, s, "k1", 8) // ts3, overwrite -> slot0 live = k2 only
	putBytes(t, s, "k3", 8) // ts4, slot1 full

	// Force an empty tail segment, then clean into it: slot0 (live 26,
	// score 26*2/78 = 2/3) beats slot1 (live 52, score 0).
	if err := s.ensureSpace(52); err != nil {
		t.Fatalf("ensureSpace: %v", err)
	}
	if err := s.cleanOne(); err != nil {
		t.Fatalf("cleanOne: %v", err)
	}
	mustVerify(t, s)

	segs := s.Segments()
	t.Logf("output layout after migration:\n%s", s.Dump())
	if len(segs) != 2 || segs[1].ID != 2 {
		t.Fatalf("want slot2 as migration target, got %+v", segs)
	}
	migrated := segs[1]
	if len(migrated.Blocks) != 1 || migrated.Blocks[0].Key != "k2" {
		t.Fatalf("slot2 should hold migrated k2, got %+v", migrated.Blocks)
	}
	if got := migrated.Blocks[0].TS; got != 2 {
		t.Fatalf("migrated block ts = %d, want original ts 2", got)
	}
	// clock is 4 (four appends); if migration had stamped the block with the
	// migration time, maxTS would be 4 and age 0. Original ts 2 -> age 2.
	if migrated.MaxTS != 2 {
		t.Fatalf("segment maxTS = %d, want 2 (age must not reset)", migrated.MaxTS)
	}
	t.Logf("decision check: clock=4, migrated maxTS=%d -> age=%d (not reset to 0)",
		migrated.MaxTS, 4-migrated.MaxTS)
}

// TestSpaceExhausted checks that a write is rejected as a whole when cleaning
// cannot yield a net free slot, and that rejected writes append nothing.
func TestSpaceExhausted(t *testing.T) {
	s := newStore(t, 52, 2)
	putBytes(t, s, "k1", 8) // ts1, slot0
	putBytes(t, s, "k2", 8) // ts2, slot0 full
	putBytes(t, s, "k3", 8) // ts3, slot1
	putBytes(t, s, "k4", 8) // ts4, slot1 full

	before := s.Dump()
	// Both slots are full of live blocks; the only victim (slot0) has 52
	// live bytes but the tail has 0 remaining: migration would consume the
	// freed slot, so there is no net gain -> reject.
	err := s.Put("k5", make([]byte, 8))
	t.Logf("input: Put(k5) -> output: %v (victim live=52 > tail remaining=0, no net free slot)", err)
	if !errors.Is(err, ErrSpaceExhausted) {
		t.Fatalf("got %v, want ErrSpaceExhausted", err)
	}
	if got := s.Dump(); got != before {
		t.Fatalf("rejected write must not change layout:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if _, ok := s.Get("k5"); ok {
		t.Fatal("rejected write must not be readable")
	}
	if err := s.Delete("k1"); !errors.Is(err, ErrSpaceExhausted) {
		t.Fatalf("delete tombstone needs space too: got %v, want ErrSpaceExhausted", err)
	}
	mustVerify(t, s)

	// A store with a single slot has no sealed segment to clean at all.
	single := newStore(t, 52, 1)
	putBytes(t, single, "k1", 8)
	putBytes(t, single, "k2", 8)
	if err := single.Put("k3", make([]byte, 8)); !errors.Is(err, ErrSpaceExhausted) {
		t.Fatalf("single slot: got %v, want ErrSpaceExhausted", err)
	}
	t.Log("input: Put(k3) with MaxSegments=1 -> output: ErrSpaceExhausted (no sealed segment)")
}
