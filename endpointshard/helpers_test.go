package endpointshard

import (
	"fmt"
	"sort"
	"testing"
)

func ep(id, region string, healthy, terminating bool) Endpoint {
	return Endpoint{ID: id, Region: region, Healthy: healthy, Terminating: terminating}
}

// live is shorthand for a healthy, non-terminating endpoint.
func live(id, region string) Endpoint { return ep(id, region, true, false) }

func mustCreate(t *testing.T, m *Manager, name string, capacity int) {
	t.Helper()
	if err := m.CreateService(name, capacity); err != nil {
		t.Fatalf("CreateService(%q, %d) failed: %v", name, capacity, err)
	}
}

func mustSync(t *testing.T, m *Manager, name string, desired []Endpoint) *SyncReport {
	t.Helper()
	rep, err := m.Sync(name, desired)
	if err != nil {
		t.Fatalf("Sync(%q) failed: %v", name, err)
	}
	return rep
}

// shardMap renders the service as shard number -> sorted endpoint IDs.
func shardMap(t *testing.T, m *Manager, name string) map[int][]string {
	t.Helper()
	infos, err := m.Inspect(name)
	if err != nil {
		t.Fatalf("Inspect(%q) failed: %v", name, err)
	}
	out := make(map[int][]string, len(infos))
	for _, info := range infos {
		ids := make([]string, 0, len(info.Endpoints))
		for _, e := range info.Endpoints {
			ids = append(ids, e.ID)
		}
		out[info.Num] = ids
	}
	return out
}

// gens renders the service as shard number -> modification generation.
func gens(t *testing.T, m *Manager, name string) map[int]uint64 {
	t.Helper()
	infos, err := m.Inspect(name)
	if err != nil {
		t.Fatalf("Inspect(%q) failed: %v", name, err)
	}
	out := make(map[int]uint64, len(infos))
	for _, info := range infos {
		out[info.Num] = info.Generation
	}
	return out
}

func fmtShardMap(sm map[int][]string) string {
	nums := make([]int, 0, len(sm))
	for n := range sm {
		nums = append(nums, n)
	}
	sort.Ints(nums)
	s := "{"
	for i, n := range nums {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%d:%v", n, sm[n])
	}
	return s + "}"
}

// assertShards checks the exact shard layout and logs the basis.
func assertShards(t *testing.T, m *Manager, name string, want map[int][]string) {
	t.Helper()
	got := shardMap(t, m, name)
	if fmtShardMap(got) != fmtShardMap(want) {
		t.Fatalf("shard layout mismatch: got %s, want %s", fmtShardMap(got), fmtShardMap(want))
	}
	t.Logf("判定依据: shard layout %s == expected %s", fmtShardMap(got), fmtShardMap(want))
}

// reportView renders a report as shard number -> (generation, deleted).
func reportView(rep *SyncReport) map[int][2]uint64 {
	out := make(map[int][2]uint64, len(rep.Changes))
	for _, c := range rep.Changes {
		del := uint64(0)
		if c.Deleted {
			del = 1
		}
		out[c.ShardNum] = [2]uint64{c.Generation, del}
	}
	return out
}

// assertReport checks the exact reported shard set, generations and
// deletion flags. want maps shard number -> [generation, deleted].
func assertReport(t *testing.T, rep *SyncReport, want map[int][2]uint64) {
	t.Helper()
	got := reportView(rep)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("report mismatch: got %v, want %v", got, want)
	}
	t.Logf("判定依据: report %v == expected %v", got, want)
}

func assertGens(t *testing.T, m *Manager, name string, want map[int]uint64) {
	t.Helper()
	got := gens(t, m, name)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("generations mismatch: got %v, want %v", got, want)
	}
	t.Logf("判定依据: generations %v == expected %v", got, want)
}

func endpointIDs(eps []Endpoint) []string {
	ids := make([]string, 0, len(eps))
	for _, e := range eps {
		ids = append(ids, e.ID)
	}
	return ids
}
