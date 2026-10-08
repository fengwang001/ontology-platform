package endpointshard

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

// TestRandomizedDifferential runs a long random sequence of
// create/delete/sync/resize/query operations against both the real
// Manager and the independent naive model, comparing every output
// after every step. Each step logs its input, the actual output and
// the assertion basis (visible with `go test -v`).
func TestRandomizedDifferential(t *testing.T) {
	seed := int64(1619)
	if v := os.Getenv("ENDPOINTSHARD_SEED"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			t.Fatalf("bad ENDPOINTSHARD_SEED: %v", err)
		}
		seed = parsed
	}
	t.Logf("随机种子: %d (可用 ENDPOINTSHARD_SEED 复现)", seed)
	rng := rand.New(rand.NewSource(seed))
	real := NewManager()
	naive := newNaiveManager()
	services := []string{"alpha", "beta", "gamma"}
	regions := []string{"r1", "r2", "r3"}
	endpointIDs := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		endpointIDs = append(endpointIDs, fmt.Sprintf("e%02d", i))
	}
	randomDesired := func() []Endpoint {
		n := rng.Intn(13)
		perm := rng.Perm(len(endpointIDs))[:n]
		out := make([]Endpoint, 0, n)
		for _, idx := range perm {
			out = append(out, Endpoint{
				ID:          endpointIDs[idx],
				Region:      regions[rng.Intn(len(regions))],
				Healthy:     rng.Intn(4) != 0,
				Terminating: rng.Intn(3) == 0,
			})
		}
		return out
	}
	checkSnapshots := func(step int, name string) {
		t.Helper()
		got, gotErr := real.Inspect(name)
		nSvc, nOk := naive.services[name]
		if (gotErr == nil) != nOk {
			t.Fatalf("step %d: existence mismatch for %q: real err=%v, naive exists=%v", step, name, gotErr, nOk)
		}
		if !nOk {
			return
		}
		want := nSvc.snapshot()
		if !snapshotsEqual(got, want) {
			t.Fatalf("step %d: snapshot mismatch for %q:\nreal:  %v\nnaive: %v", step, name, got, want)
		}
		t.Logf("判定依据: step %d 快照一致: %s", step, fmtShardMap(shardMap(t, real, name)))
	}
	const steps = 3000
	for step := 0; step < steps; step++ {
		name := services[rng.Intn(len(services))]
		switch dice := rng.Intn(100); {
		case dice < 6:
			capacity := 1 + rng.Intn(6)
			realErr := real.CreateService(name, capacity)
			naiveErr := naive.create(name, capacity)
			t.Logf("step %d 输入: CreateService(%q, %d) 实际输出: real=%v naive=%v", step, name, capacity, realErr, naiveErr)
			if errKind(realErr) != errKind(naiveErr) {
				t.Fatalf("step %d: CreateService error kind mismatch: real=%v naive=%v", step, realErr, naiveErr)
			}
			t.Logf("判定依据: step %d 错误类别一致 (%v)", step, errKind(realErr))
		case dice < 9:
			realErr := real.DeleteService(name)
			naiveErr := naive.delete(name)
			t.Logf("step %d 输入: DeleteService(%q) 实际输出: real=%v naive=%v", step, name, realErr, naiveErr)
			if errKind(realErr) != errKind(naiveErr) {
				t.Fatalf("step %d: DeleteService error kind mismatch: real=%v naive=%v", step, realErr, naiveErr)
			}
			t.Logf("判定依据: step %d 错误类别一致 (%v)", step, errKind(realErr))
		case dice < 18:
			capacity := 1 + rng.Intn(6)
			realRep, realErr := real.Resize(name, capacity)
			naiveRep, naiveErr := naive.resize(name, capacity)
			t.Logf("step %d 输入: Resize(%q, %d) 实际输出: real=%v naive=%v", step, name, capacity, realErr, naiveErr)
			if errKind(realErr) != errKind(naiveErr) {
				t.Fatalf("step %d: Resize error kind mismatch: real=%v naive=%v", step, realErr, naiveErr)
			}
			if realErr == nil && !reportsEqual(realRep, naiveRep) {
				t.Fatalf("step %d: Resize report mismatch:\nreal:  %+v\nnaive: %+v", step, realRep.Changes, naiveRep.Changes)
			}
			if realErr == nil {
				t.Logf("判定依据: step %d Resize 报告一致 (%d 个变更分片)", step, len(realRep.Changes))
			} else {
				t.Logf("判定依据: step %d 错误类别一致 (%v)", step, errKind(realErr))
			}
		case dice < 60:
			desired := randomDesired()
			realRep, realErr := real.Sync(name, desired)
			naiveRep, naiveErr := naive.sync(name, desired)
			t.Logf("step %d 输入: Sync(%q, %s) 实际输出: real=%v naive=%v", step, name, describeEndpoints(desired), realErr, naiveErr)
			if errKind(realErr) != errKind(naiveErr) {
				t.Fatalf("step %d: Sync error kind mismatch: real=%v naive=%v", step, realErr, naiveErr)
			}
			if realErr == nil && !reportsEqual(realRep, naiveRep) {
				t.Fatalf("step %d: Sync report mismatch:\nreal:  %+v\nnaive: %+v", step, realRep.Changes, naiveRep.Changes)
			}
			if realErr == nil {
				t.Logf("判定依据: step %d Sync 报告一致 (%d 个变更分片)", step, len(realRep.Changes))
			}
		default:
			region := regions[rng.Intn(len(regions))]
			realRes, realErr := real.Query(name, region)
			naiveRes, naiveErr := naive.query(name, region)
			t.Logf("step %d 输入: Query(%q, %q) 实际输出: real=%v naive=%v", step, name, region, realErr, naiveErr)
			if errKind(realErr) != errKind(naiveErr) {
				t.Fatalf("step %d: Query error kind mismatch: real=%v naive=%v", step, realErr, naiveErr)
			}
			if realErr == nil && !queryEqual(realRes, naiveRes) {
				t.Fatalf("step %d: Query result mismatch:\nreal:  %+v\nnaive: %+v", step, realRes, naiveRes)
			}
			if realErr == nil {
				t.Logf("判定依据: step %d Query 结果一致 (%d 个端点, fallback=%v)", step, len(realRes.Endpoints), realRes.Fallback)
			}
		}
		for _, svc := range services {
			checkSnapshots(step, svc)
		}
	}
}
