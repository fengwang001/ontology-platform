package endpointshard

import (
	"testing"
)

// Surviving endpoints keep their shard even when status bits or the
// region change; only the content is updated.
func TestSyncStablePlacementAndContentUpdate(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)

	in1 := []Endpoint{live("a", "r1"), live("b", "r1"), live("c", "r2")}
	rep := mustSync(t, m, "s", in1)
	t.Logf("输入: Sync(s, %+v)", in1)
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c"}})
	assertReport(t, rep, map[int][2]uint64{1: {1, 0}, 2: {1, 0}})

	// a becomes unhealthy, c changes region and starts terminating:
	// neither may leave its shard.
	in2 := []Endpoint{
		ep("a", "r1", false, false),
		live("b", "r1"),
		ep("c", "r9", true, true),
	}
	rep = mustSync(t, m, "s", in2)
	t.Logf("输入: Sync(s, %+v)", in2)
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"c"}})
	assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 0}})

	infos, err := m.Inspect("s")
	if err != nil {
		t.Fatal(err)
	}
	gotA := infos[0].Endpoints[0]
	if gotA.Healthy || gotA.Terminating {
		t.Fatalf("endpoint a content not updated in place: %+v", gotA)
	}
	gotC := infos[1].Endpoints[0]
	if gotC.Region != "r9" || !gotC.Terminating {
		t.Fatalf("endpoint c content not updated in place: %+v", gotC)
	}
	t.Logf("判定依据: a 与 c 留在原分片且内容为最新值")
}

// New endpoints go to the fullest non-full shard; a new shard is
// created only when every shard is full.
func TestSyncPlacementFullestNonFull(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 4)

	in := []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"), live("d", "r"),
		live("e", "r"), live("f", "r"), live("g", "r")}
	mustSync(t, m, "s", in)
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d"}, 2: {"e", "f", "g"}})

	// shard2 (3/4) is the fullest non-full shard: h lands there.
	in = append(in, live("h", "r"))
	rep := mustSync(t, m, "s", in)
	t.Logf("输入: Sync(s, 8 endpoints)")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d"}, 2: {"e", "f", "g", "h"}})
	assertReport(t, rep, map[int][2]uint64{2: {2, 0}})

	// All shards full: i triggers creation of shard 3.
	in = append(in, live("i", "r"))
	rep = mustSync(t, m, "s", in)
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d"}, 2: {"e", "f", "g", "h"}, 3: {"i"}})
	assertReport(t, rep, map[int][2]uint64{3: {1, 0}})
}

// Placement ties (several shards with the same maximal non-full size)
// are broken by the lowest shard number; multiple additions in one
// sync are placed in ascending ID order.
func TestSyncPlacementTieBreaks(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 3)

	in := []Endpoint{live("a", "r"), live("b", "r"), live("d", "r"), live("e", "r"),
		live("g", "r"), live("h", "r")}
	mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"),
		live("d", "r"), live("e", "r"), live("f", "r"),
		live("g", "r"), live("h", "r"), live("i", "r")})
	// Remove c, f, i: three shards of size 2, no merge (2+2 > 3).
	rep := mustSync(t, m, "s", in)
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"d", "e"}, 3: {"g", "h"}})
	assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 0}, 3: {2, 0}})

	// x and y are added in ID order; both tie-break to shard 1, the
	// lowest-numbered fullest non-full shard, until it is full.
	rep = mustSync(t, m, "s", append(in, live("x", "r"), live("y", "r")))
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"a", "b", "x"}, 2: {"d", "e", "y"}, 3: {"g", "h"}})
	assertReport(t, rep, map[int][2]uint64{1: {3, 0}, 2: {3, 0}})
}

// Empty shards are deleted during cleanup and shard numbers are never
// reused.
func TestSyncEmptyShardDeletionAndNumberNoReuse(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)

	mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r")})
	assertShards(t, m, "s", map[int][]string{1: {"a", "b"}})

	rep := mustSync(t, m, "s", nil)
	t.Logf("输入: Sync(s, [])")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{})
	assertReport(t, rep, map[int][2]uint64{1: {2, 1}})

	// The next shard created must be number 2, not 1.
	rep = mustSync(t, m, "s", []Endpoint{live("c", "r")})
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{2: {"c"}})
	assertReport(t, rep, map[int][2]uint64{2: {1, 0}})
}

// Merge boundary: sizes summing to exactly M merge; M+1 does not.
func TestSyncMergeBoundary(t *testing.T) {
	t.Run("sum equals M merges", func(t *testing.T) {
		m := NewManager()
		mustCreate(t, m, "s", 4)
		mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"),
			live("d", "r"), live("e", "r"), live("f", "r")})
		assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c", "d"}, 2: {"e", "f"}})

		// Remove c, d: sizes 2 and 2, sum == M == 4 -> merge shard 2
		// into shard 1.
		rep := mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("e", "r"), live("f", "r")})
		t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
		assertShards(t, m, "s", map[int][]string{1: {"a", "b", "e", "f"}})
		assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 1}})
	})

	t.Run("sum above M does not merge", func(t *testing.T) {
		m := NewManager()
		mustCreate(t, m, "s", 3)
		mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("c", "r"),
			live("d", "r"), live("e", "r")})
		assertShards(t, m, "s", map[int][]string{1: {"a", "b", "c"}, 2: {"d", "e"}})

		// Remove c: sizes 2 and 2, sum 4 > M == 3 -> no merge.
		rep := mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("d", "r"), live("e", "r")})
		t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
		assertShards(t, m, "s", map[int][]string{1: {"a", "b"}, 2: {"d", "e"}})
		assertReport(t, rep, map[int][2]uint64{1: {2, 0}})
	})
}

// At most one merge happens per sync, even if a second pair would
// also fit.
func TestSyncAtMostOneMerge(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 10)

	var full []Endpoint
	for _, id := range []string{"e00", "e01", "e02", "e03", "e04", "e05", "e06", "e07", "e08", "e09",
		"e10", "e11", "e12", "e13", "e14", "e15", "e16", "e17", "e18", "e19",
		"e20", "e21", "e22", "e23", "e24"} {
		full = append(full, live(id, "r"))
	}
	mustSync(t, m, "s", full)
	assertShards(t, m, "s", map[int][]string{
		1: {"e00", "e01", "e02", "e03", "e04", "e05", "e06", "e07", "e08", "e09"},
		2: {"e10", "e11", "e12", "e13", "e14", "e15", "e16", "e17", "e18", "e19"},
		3: {"e20", "e21", "e22", "e23", "e24"},
	})

	// Keep two endpoints per shard: sizes 2, 2, 2. One merge combines
	// shards 1 and 2 into a size-4 shard; a second merge with shard 3
	// (4+2 <= 10) must NOT happen.
	keep := []Endpoint{live("e00", "r"), live("e01", "r"), live("e10", "r"), live("e11", "r"),
		live("e20", "r"), live("e21", "r")}
	rep := mustSync(t, m, "s", keep)
	t.Logf("输入: Sync(s, 6 endpoints)")
	t.Logf("实际输出: report=%v layout=%s", rep.Changed(), fmtShardMap(shardMap(t, m, "s")))
	assertShards(t, m, "s", map[int][]string{1: {"e00", "e01", "e10", "e11"}, 3: {"e20", "e21"}})
	assertReport(t, rep, map[int][2]uint64{1: {2, 0}, 2: {2, 1}, 3: {2, 0}})
}

// The report contains exactly the shards that changed; a no-op sync
// reports nothing and bumps no generation.
func TestSyncReportExactnessAndGenerations(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "s", 2)

	in := []Endpoint{live("a", "r"), live("b", "r"), live("c", "r")}
	rep := mustSync(t, m, "s", in)
	assertReport(t, rep, map[int][2]uint64{1: {1, 0}, 2: {1, 0}})
	assertGens(t, m, "s", map[int]uint64{1: 1, 2: 1})

	// Identical sync: empty report, generations frozen.
	rep = mustSync(t, m, "s", in)
	t.Logf("输入: Sync(s, identical set)")
	t.Logf("实际输出: report=%v", rep.Changed())
	if len(rep.Changes) != 0 {
		t.Fatalf("no-op sync reported changes: %v", rep.Changed())
	}
	assertGens(t, m, "s", map[int]uint64{1: 1, 2: 1})
	t.Logf("判定依据: 空报告且代次不变")

	// Only shard 2 changes (d joins it); shard 1 stays at gen 1.
	rep = mustSync(t, m, "s", append(in, live("d", "r")))
	assertReport(t, rep, map[int][2]uint64{2: {2, 0}})
	assertGens(t, m, "s", map[int]uint64{1: 1, 2: 2})

	// Only shard 2 changes again (c leaves it).
	rep = mustSync(t, m, "s", []Endpoint{live("a", "r"), live("b", "r"), live("d", "r")})
	assertReport(t, rep, map[int][2]uint64{2: {3, 0}})
	assertGens(t, m, "s", map[int]uint64{1: 1, 2: 3})
}
