package endpointshard

import (
	"fmt"
	"testing"
)

// 性能可验证性：以索引节点访问次数为客观度量（见 Stats），
// 证明同步开销不随未受影响的端点数/分片数线性增长，
// 读取开销与结果大小成正比。

func buildService(t *testing.T, name string, capPerShard, n int) *Manager {
	t.Helper()
	m := NewManager()
	mustCreate(t, m, name, capPerShard)
	ids := make([]string, 0, n)
	for i := 0; i < n; i++ {
		ids = append(ids, fmt.Sprintf("e%06d", i))
	}
	mustSync(t, m, name, readyEps(ids...))
	return m
}

func statsOf(t *testing.T, m *Manager, name string) StatsSnapshot {
	t.Helper()
	s, err := m.Stats(name)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	return s
}

// 空操作同步：报告为空，且不发生任何索引节点访问。
func TestNoopSyncHasZeroIndexVisits(t *testing.T) {
	m := buildService(t, "svc", 4, 5000)
	if err := m.ResetStats("svc"); err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, 5000)
	for i := 0; i < 5000; i++ {
		ids = append(ids, fmt.Sprintf("e%06d", i))
	}
	rep := mustSync(t, m, "svc", readyEps(ids...))
	s := statsOf(t, m, "svc")
	t.Logf("输入: 5000 端点的相同同步; 实际输出: 报告空=%v IndexVisits=%d; 判定依据: 空报告且零索引访问", rep.Empty(), s.IndexVisits)
	if !rep.Empty() || s.IndexVisits != 0 || s.QueryVisits != 0 {
		t.Fatalf("no-op sync must not touch indexes: %+v", s)
	}
}

// 单端点变更的同步开销：不随未受影响端点数量、未受影响分片数量线性增长。
// M=1 使分片数等于端点数，同时放大两个维度。
func TestSyncCostIndependentOfUnaffected(t *testing.T) {
	measure := func(n int) uint64 {
		name := fmt.Sprintf("svc-%d", n)
		m := buildService(t, name, 1, n)
		if err := m.ResetStats(name); err != nil {
			t.Fatal(err)
		}
		// 只变更一个端点的状态位。
		desired := make([]Endpoint, 0, n)
		for i := 0; i < n; i++ {
			e := readyEp(fmt.Sprintf("e%06d", i), "cn")
			if i == n/2 {
				e.Terminating = true
			}
			desired = append(desired, e)
		}
		mustSync(t, m, name, desired)
		return statsOf(t, m, name).IndexVisits
	}
	small := measure(5000)
	large := measure(50000)
	t.Logf("输入: 单端点状态变更; 实际输出: n=5000 时 %d 次访问, n=50000 时 %d 次; 判定依据: 至多对数增长", small, large)
	if large > 3*small+100 {
		t.Fatalf("sync cost grows too fast: n=5000 -> %d, n=50000 -> %d", small, large)
	}
}

// 读取开销与结果大小成正比：查询访问的节点数恰好等于结果大小。
func TestQueryCostProportionalToResult(t *testing.T) {
	m := NewManager()
	mustCreate(t, m, "svc", 7)
	// 300 个就绪端点 + 200 个终止中端点 + 100 个不健康端点。
	var desired []Endpoint
	for i := 0; i < 300; i++ {
		desired = append(desired, readyEp(fmt.Sprintf("r%04d", i), "cn"))
	}
	for i := 0; i < 200; i++ {
		desired = append(desired, ep(fmt.Sprintf("t%04d", i), "us", true, true))
	}
	for i := 0; i < 100; i++ {
		desired = append(desired, ep(fmt.Sprintf("u%04d", i), "eu", false, false))
	}
	mustSync(t, m, "svc", desired)

	if err := m.ResetStats("svc"); err != nil {
		t.Fatal(err)
	}
	res := mustQuery(t, m, "svc", "cn")
	s := statsOf(t, m, "svc")
	t.Logf("输入: 300 就绪的查询; 实际输出: %d 端点, %d 次访问; 判定依据: 访问数 == 结果大小", len(res.Endpoints), s.QueryVisits)
	if len(res.Endpoints) != 300 || s.QueryVisits != uint64(len(res.Endpoints)) {
		t.Fatalf("query visits %d must equal result size %d", s.QueryVisits, len(res.Endpoints))
	}

	// 回退路径同样与结果大小成正比。
	for i := range desired {
		desired[i].Healthy = true
		desired[i].Terminating = true
	}
	mustSync(t, m, "svc", desired)
	if err := m.ResetStats("svc"); err != nil {
		t.Fatal(err)
	}
	res = mustQuery(t, m, "svc", "cn")
	s = statsOf(t, m, "svc")
	t.Logf("输入: 600 可服务且终止中的回退查询; 实际输出: %d 端点, %d 次访问; 判定依据: 访问数 == 结果大小", len(res.Endpoints), s.QueryVisits)
	if len(res.Endpoints) != 600 || s.QueryVisits != uint64(len(res.Endpoints)) {
		t.Fatalf("fallback query visits %d must equal result size %d", s.QueryVisits, len(res.Endpoints))
	}
}

// 基准：单端点变更的同步开销随规模的变化（应近似对数级）。
// 运行: go test ./endpointshard -bench Sync -benchmem
func BenchmarkSyncSingleEndpointChange(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := NewManager()
			if err := m.CreateService("svc", 4); err != nil {
				b.Fatal(err)
			}
			base := make([]Endpoint, 0, n)
			for i := 0; i < n; i++ {
				base = append(base, readyEp(fmt.Sprintf("e%06d", i), "cn"))
			}
			if _, err := m.Sync("svc", base); err != nil {
				b.Fatal(err)
			}
			flip := make([]Endpoint, n)
			copy(flip, base)
			flip[n/2].Terminating = true
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i%2 == 0 {
					if _, err := m.Sync("svc", flip); err != nil {
						b.Fatal(err)
					}
				} else {
					if _, err := m.Sync("svc", base); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}

// 基准：查询开销与结果大小的关系。
// 运行: go test ./endpointshard -bench Query -benchmem
func BenchmarkQuery(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := NewManager()
			if err := m.CreateService("svc", 64); err != nil {
				b.Fatal(err)
			}
			base := make([]Endpoint, 0, n)
			for i := 0; i < n; i++ {
				base = append(base, readyEp(fmt.Sprintf("e%06d", i), "cn"))
			}
			if _, err := m.Sync("svc", base); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := m.Query("svc", "cn"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
