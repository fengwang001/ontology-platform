package endpointshard

import "testing"

func ep(id, region string, healthy, terminating bool) Endpoint {
	return Endpoint{ID: id, Region: region, Healthy: healthy, Terminating: terminating}
}

// readyEp 构造一个就绪端点（健康且非终止中）。
func readyEp(id, region string) Endpoint { return ep(id, region, true, false) }

func readyEps(ids ...string) []Endpoint {
	out := make([]Endpoint, 0, len(ids))
	for _, id := range ids {
		out = append(out, readyEp(id, "cn"))
	}
	return out
}

func mustCreate(t *testing.T, m *Manager, name string, capPerShard int) {
	t.Helper()
	if err := m.CreateService(name, capPerShard); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func mustSync(t *testing.T, m *Manager, name string, desired []Endpoint) ChangeReport {
	t.Helper()
	rep, err := m.Sync(name, desired)
	if err != nil {
		t.Fatalf("sync %s: %v", name, err)
	}
	return rep
}

func mustDescribe(t *testing.T, m *Manager, name string) ServiceView {
	t.Helper()
	v, err := m.Describe(name)
	if err != nil {
		t.Fatalf("describe %s: %v", name, err)
	}
	return v
}

// shardOf 返回分片中的端点标识列表（已按字典序）。
func shardMembers(t *testing.T, v ServiceView, shardID int) []string {
	t.Helper()
	for _, sh := range v.Shards {
		if sh.ID == shardID {
			ids := make([]string, 0, len(sh.Endpoints))
			for _, e := range sh.Endpoints {
				ids = append(ids, e.ID)
			}
			return ids
		}
	}
	t.Fatalf("shard %d not found in view %+v", shardID, v)
	return nil
}

func shardGen(t *testing.T, v ServiceView, shardID int) uint64 {
	t.Helper()
	for _, sh := range v.Shards {
		if sh.ID == shardID {
			return sh.Generation
		}
	}
	t.Fatalf("shard %d not found in view %+v", shardID, v)
	return 0
}

// endpointShard 返回端点所在分片编号；不存在时返回 0。
func endpointShard(v ServiceView, id string) int {
	for _, sh := range v.Shards {
		for _, e := range sh.Endpoints {
			if e.ID == id {
				return sh.ID
			}
		}
	}
	return 0
}

func findChange(rep ChangeReport, shardID int) (ShardChange, bool) {
	for _, c := range rep.Shards {
		if c.ShardID == shardID {
			return c, true
		}
	}
	return ShardChange{}, false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func idsOf(eps []Endpoint) []string {
	out := make([]string, 0, len(eps))
	for _, e := range eps {
		out = append(out, e.ID)
	}
	return out
}
