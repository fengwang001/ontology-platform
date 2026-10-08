package endpointshard

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentLinearizable hammers one Manager with concurrent
// Sync/Resize/Query calls on several pre-created services. Because
// every mutating call holds its service lock for its whole duration,
// the completion order of mutations is a valid serialization: the
// test records every mutation on completion, replays them in that
// order through the naive model, and requires every report and the
// final state to match exactly. Every observed Query result must be a
// complete view of some state the service actually passed through.
func TestConcurrentLinearizable(t *testing.T) {
	m := NewManager()
	services := []string{"s0", "s1", "s2", "s3"}
	for _, s := range services {
		mustCreate(t, m, s, 4)
	}
	type mutation struct {
		op       string
		svc      string
		desired  []Endpoint
		capacity int
		report   *SyncReport
	}
	type observation struct {
		svc    string
		region string
		result *QueryResult
	}
	var mu sync.Mutex
	var mutations []mutation
	var observations []observation
	// recMu serializes call+record per service, so the recorded
	// completion order of same-service mutations equals their
	// application order and is therefore a valid serialization.
	recMu := map[string]*sync.Mutex{}
	desiredHistory := map[string]map[string]bool{}
	for _, s := range services {
		recMu[s] = &sync.Mutex{}
		desiredHistory[s] = map[string]bool{queryKey(naiveQueryOf(nil, "r1")): true}
	}
	regions := []string{"r1", "r2"}
	endpointIDs := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		endpointIDs = append(endpointIDs, fmt.Sprintf("e%02d", i))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g*7919 + 13)))
			for i := 0; i < 100; i++ {
				svc := services[rng.Intn(len(services))]
				switch dice := rng.Intn(10); {
				case dice < 2:
					capacity := 1 + rng.Intn(8)
					recMu[svc].Lock()
					rep, err := m.Resize(svc, capacity)
					if err != nil {
						recMu[svc].Unlock()
						t.Errorf("Resize(%q, %d) failed: %v", svc, capacity, err)
						return
					}
					mu.Lock()
					mutations = append(mutations, mutation{op: "resize", svc: svc, capacity: capacity, report: rep})
					mu.Unlock()
					recMu[svc].Unlock()
				case dice < 5:
					region := regions[rng.Intn(len(regions))]
					res, err := m.Query(svc, region)
					if err != nil {
						t.Errorf("Query(%q, %q) failed: %v", svc, region, err)
						return
					}
					mu.Lock()
					observations = append(observations, observation{svc: svc, region: region, result: res})
					mu.Unlock()
				default:
					n := rng.Intn(9)
					perm := rng.Perm(len(endpointIDs))[:n]
					desired := make([]Endpoint, 0, n)
					for _, idx := range perm {
						desired = append(desired, Endpoint{
							ID:          endpointIDs[idx],
							Region:      regions[rng.Intn(len(regions))],
							Healthy:     rng.Intn(4) != 0,
							Terminating: rng.Intn(3) == 0,
						})
					}
					recMu[svc].Lock()
					rep, err := m.Sync(svc, desired)
					if err != nil {
						recMu[svc].Unlock()
						t.Errorf("Sync(%q) failed: %v", svc, err)
						return
					}
					mu.Lock()
					mutations = append(mutations, mutation{op: "sync", svc: svc, desired: desired, report: rep})
					desiredHistory[svc][queryKey(naiveQueryOf(desired, "r1"))] = true
					desiredHistory[svc][queryKey(naiveQueryOf(desired, "r2"))] = true
					mu.Unlock()
					recMu[svc].Unlock()
				}
			}
		}(g)
	}
	wg.Wait()
	t.Logf("并发完成: %d 个变更操作, %d 次查询", len(mutations), len(observations))
	// Replay all mutations in completion order through the naive
	// model; every report must match the one the real Manager
	// returned for that call.
	naive := newNaiveManager()
	for _, s := range services {
		if err := naive.create(s, 4); err != nil {
			t.Fatal(err)
		}
	}
	for i, mut := range mutations {
		var nrep *SyncReport
		var err error
		if mut.op == "sync" {
			nrep, err = naive.sync(mut.svc, mut.desired)
		} else {
			nrep, err = naive.resize(mut.svc, mut.capacity)
		}
		if err != nil {
			t.Fatalf("mutation %d (%s on %q) replay failed: %v", i, mut.op, mut.svc, err)
		}
		if !reportsEqual(mut.report, nrep) {
			t.Fatalf("mutation %d (%s on %q): report mismatch:\nreal:  %+v\nnaive: %+v",
				i, mut.op, mut.svc, mut.report.Changes, nrep.Changes)
		}
	}
	t.Logf("判定依据: 全部 %d 个变更报告与按完成顺序重放的朴素模型一致", len(mutations))
	// The final state of every service must equal the replayed model.
	for _, svc := range services {
		got, err := m.Inspect(svc)
		if err != nil {
			t.Fatal(err)
		}
		want := naive.services[svc].snapshot()
		if !snapshotsEqual(got, want) {
			t.Fatalf("final state mismatch for %q:\nreal:  %v\nnaive: %v", svc, got, want)
		}
		t.Logf("判定依据: 服务 %q 最终状态 %s 与串行重放一致", svc, fmtShardMap(shardMap(t, m, svc)))
	}
	// Every query observed a complete view of a state the service
	// actually passed through (some synced desired set, or empty).
	for i, ob := range observations {
		key := queryKey(ob.result)
		if !desiredHistory[ob.svc][key] {
			t.Fatalf("observation %d on %q is not a view of any synced state: %v", i, ob.svc, key)
		}
	}
	t.Logf("判定依据: 全部 %d 次查询结果均为某个完整同步状态的视图", len(observations))
}

// naiveQueryOf computes the expected query result for a plain desired
// set, independent of any service state.
func naiveQueryOf(desired []Endpoint, region string) *QueryResult {
	svc := newNaiveService(1 << 30)
	svc.sync(desired)
	return svc.query(region)
}

// queryKey renders a query result canonically for set membership.
func queryKey(res *QueryResult) string {
	s := fmt.Sprintf("fallback=%v|", res.Fallback)
	for _, e := range res.Endpoints {
		s += fmt.Sprintf("%s@%s(h=%v,t=%v);", e.ID, e.Region, e.Healthy, e.Terminating)
	}
	return s
}
