package endpointshard

import (
	"testing"
)

// Growing M never moves endpoints; it only affects future placement
// and merge decisions.
func TestResizeGrowNoMovement(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	in := []Endpoint{live("a", "r"), live("b", "r"), live("c", "r")}
	mustSync(t, m, "s", in)
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c"}})
	rep, err := m.Resize("s", 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Resize(s, 5)")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	if len(rep.Changes) != 0 {
		t.Fatalf("grow reported changes: %v", rep.Changed())
	}
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c"}})
	assertGens(t, m, "s", map[int]uint64{1: 1, 2: 1})
	// New placements prefer the fullest non-full shard under the new
	// capacity: d and e join shard 1 (2/5 over 1/5), then the merge
	// judgement uses the new M: sizes 4 and 1 sum to 5 <= 5, so
	// shard 2 is merged into shard 1.
	rep = mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"),
		live("d", "r"), live("e", "r")})
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d", "e"}})
	assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 1}})
	rep = mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r")})
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}})
	assertReport(t, rep, map[int][2]uint64{1: {3, 0}})
}

// Shrinking M evicts the lexicographically largest endpoints of
// over-capacity shards and re-places them with the placement rule, in
// ascending ID order, atomically.
func TestResizeShrinkEvictsAndReplaces(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 4)
	in := []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"), live("d", "r"), live("e", "r")}
	mustSync(t, m, "s", in)
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d"}, 2: {"e"}})
	// Shrink to 2: shard 1 evicts d then c (largest first). c joins
	// shard 2 (fullest non-full), d creates shard 3.
	rep, err := m.Resize("s", 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Resize(s, 2)")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c", "e"}, 3: {"d"}})
	assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 0}, 3: {1, 0}})
}

// A shrink can cascade: re-placed endpoints may fill shards so that
// further evicted endpoints create new shards, until nothing is over
// capacity.
func TestResizeShrinkCascade(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 3)
	in := []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"),
		live("d", "r"), live("e", "r"), live("f", "r")}
	mustSync(t, m, "s", in)
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c"}, 2: {"d", "e", "f"}})
	// Shrink to 1: shard 1 evicts c, b; shard 2 evicts f, e.
	// Re-placed in ID order: b -> shard 3, c -> shard 4, e -> shard 5,
	// f -> shard 6 (every existing shard is full at size 1).
	rep, err := m.Resize("s", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Resize(s, 1)")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{
		1: {"a"}, 2: {"d"}, 3: {"b"}, 4: {"c"}, 5: {"e"}, 6: {"f"},
	})
	assertReport(t, rep, map[int][2]uint64{
		1: {2, 0}, 2: {2, 0}, 3: {1, 0}, 4: {1, 0}, 5: {1, 0}, 6: {1, 0},
	})
}

// Resizing to the current capacity is a no-op; non-positive
// capacities are rejected without touching state.
func TestResizeNoopAndInvalid(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)
	mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r")})
	rep, err := m.Resize("s", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Changes) != 0 {
		t.Fatalf("same-capacity resize reported changes: %v", rep.Changed())
	}
	assertGens(t, m, "s", map[int]uint64{1: 1})
	for _, bad := range []int{0, -1, -100} {
		_, err := m.Resize("s", bad)
		if err == nil {
			t.Fatalf("Resize(s, %d) succeeded", bad)
		}
		kind, _ := KindOf(err)
		t.Logf("输入: Resize(s, %d) 实际输出: %v 判定依据: kind==%v", bad, err, kind)
		if kind != ErrInvalidArgument {
			t.Fatalf("Resize(s, %d) kind = %v, want %v", bad, kind, ErrInvalidArgument)
		}
	}
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}})
	assertGens(t, m, "s", map[int]uint64{1: 1})
}
