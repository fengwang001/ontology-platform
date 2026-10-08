package endpointshard

import (
	"fmt"
	"testing"
)

// These tests prove the cost model with deterministic operation
// counters (Manager.Stats) instead of wall-clock timing, so they
// cannot flake. Benchmarks at the bottom complement them with
// measurable timings.

func buildService(t *testing.T, m *Manager, name string, capacity, endpoints int) []Endpoint {
	t.Helper()
	mustCreate(t, m, name, capacity)
	desired := make([]Endpoint, 0, endpoints)
	for i := 0; i < endpoints; i++ {
		desired = append(desired, live(fmt.Sprintf("e%06d", i), "r1"))
	}
	mustSync(t, m, name, desired)
	return desired
}

// A sync that changes one endpoint touches a constant number of
// shards and a logarithmic number of size-index nodes, no matter how
// many endpoints and shards are unaffected.
func TestSyncCostIndependentOfUnaffectedState(t *testing.T) {
	m := NewManager()
	small := buildService(t, m, "small", 2, 200)   // 100 shards
	large := buildService(t, m, "large", 2, 20000) // 10000 shards
	flip := func(eps []Endpoint) []Endpoint {
		out := make([]Endpoint, len(eps))
		copy(out, eps)
		out[0].Healthy = !out[0].Healthy
		return out
	}
	m.ResetStats()
	repSmall := mustSync(t, m, "small", flip(small))
	statSmall := m.Stats()
	t.Logf("输入: Sync(small, 200 端点中翻转 1 个健康位) 实际输出: report=%v stats=%+v",
		repSmall.Changed(), statSmall)
	m.ResetStats()
	repLarge := mustSync(t, m, "large", flip(large))
	statLarge := m.Stats()
	t.Logf("输入: Sync(large, 20000 端点中翻转 1 个健康位) 实际输出: report=%v stats=%+v",
		repLarge.Changed(), statLarge)
	if len(repSmall.Changes) != 1 || len(repLarge.Changes) != 1 {
		t.Fatalf("single-endpoint change must report exactly one shard: small=%v large=%v",
			repSmall.Changed(), repLarge.Changed())
	}
	if statSmall.ShardsTouched != 1 || statLarge.ShardsTouched != 1 {
		t.Fatalf("single-endpoint change must touch exactly one shard: small=%d large=%d",
			statSmall.ShardsTouched, statLarge.ShardsTouched)
	}
	// The size-index work grows at most logarithmically with the
	// shard count (100x more shards, far less than 100x more visits).
	if statLarge.SizeIndexNodeVisits > 10*statSmall.SizeIndexNodeVisits+50 {
		t.Fatalf("size index visits grow faster than logarithmic: small=%d large=%d",
			statSmall.SizeIndexNodeVisits, statLarge.SizeIndexNodeVisits)
	}
	if statLarge.SizeIndexNodeVisits > 500 {
		t.Fatalf("size index visits not logarithmic in shard count: %d", statLarge.SizeIndexNodeVisits)
	}
	t.Logf("判定依据: 100 倍分片数下, 触及分片数恒为 1, 索引节点访问 %d -> %d (对数增长)",
		statSmall.SizeIndexNodeVisits, statLarge.SizeIndexNodeVisits)
}

// A sync whose change set is a pure addition of one endpoint likewise
// stays constant in the number of affected shards, even when the
// placement triggers cleanup and the merge check.
func TestSyncPlacementCostIndependentOfShardCount(t *testing.T) {
	m := NewManager()
	desired := buildService(t, m, "s", 3, 30000) // 10000 full shards
	m.ResetStats()
	rep := mustSync(t, m, "s", append(desired, live("zzzzzz", "r1")))
	stats := m.Stats()
	t.Logf("输入: Sync(s, +1 端点, 10000 个满分片) 实际输出: report=%v stats=%+v", rep.Changed(), stats)
	if len(rep.Changes) != 1 {
		t.Fatalf("expected exactly one created shard, got %v", rep.Changed())
	}
	if stats.ShardsTouched != 1 {
		t.Fatalf("placement touched %d shards, want 1", stats.ShardsTouched)
	}
	if stats.SizeIndexNodeVisits > 500 {
		t.Fatalf("placement index visits not logarithmic: %d", stats.SizeIndexNodeVisits)
	}
	t.Logf("判定依据: 新增落位只触及新建分片, 索引访问 %d 次 (对数级)", stats.SizeIndexNodeVisits)
}

// Query scans exactly the endpoints that can appear in its result.
func TestQueryCostProportionalToResult(t *testing.T) {
	m := NewManager()
	const total = 20000
	desired := make([]Endpoint, 0, total)
	for i := 0; i < total; i++ {
		// All unhealthy except three ready endpoints.
		e := ep(fmt.Sprintf("e%06d", i), "r1", false, false)
		if i < 3 {
			e = live(fmt.Sprintf("e%06d", i), "r1")
		}
		desired = append(desired, e)
	}
	mustCreate(t, m, "s", 4)
	mustSync(t, m, "s", desired)
	m.ResetStats()
	res, err := m.Query("s", "r1")
	if err != nil {
		t.Fatal(err)
	}
	stats := m.Stats()
	t.Logf("输入: Query(s, r1) 实际输出: %d 个端点, QueryEndpointsSeen=%d", len(res.Endpoints), stats.QueryEndpointsSeen)
	if len(res.Endpoints) != 3 {
		t.Fatalf("expected 3 ready endpoints, got %d", len(res.Endpoints))
	}
	if stats.QueryEndpointsSeen != int64(len(res.Endpoints)) {
		t.Fatalf("query scanned %d endpoints for a result of %d", stats.QueryEndpointsSeen, len(res.Endpoints))
	}
	t.Logf("判定依据: 扫描端点数 (%d) == 结果大小 (%d), 与 %d 个非就绪端点无关",
		stats.QueryEndpointsSeen, len(res.Endpoints), total-3)
}
