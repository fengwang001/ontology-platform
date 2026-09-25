package api_test

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/api"
)

var base = [][4]int64{{0, 10, 10, 20}, {10, 20, 30, 40}, {20, 30, 50, 60}, {30, 40, 15, 25}, {40, 50, 60, 80}}

func buildBase(t *testing.T) *api.Pruner {
	t.Helper()
	pr := api.New()
	for i, r := range base {
		if err := pr.AddPartition(fmt.Sprintf("P%d", i), r[0], r[1], r[2], r[3]); err != nil {
			t.Fatalf("AddPartition: %v", err)
		}
	}
	return pr
}

func boxOf(id string) [4]int64 { return base[int(id[1]-'0')] }

// TestSoundness 不变量1+2：被裁者边界盒至少一维无交集；扫描者两维都相交。
func TestSoundness(t *testing.T) {
	pr := buildBase(t)
	cases := []struct {
		name               string
		plo, phi, vlo, vhi int64
	}{
		{"given", 20, 50, 30, 60}, {"touch-p", 10, 30, 40, 50},
		{"touch-v", 0, 50, 25, 30}, {"wide", -100, 100, -100, 100},
		{"narrow", 24, 26, 55, 56}, {"gap", 100, 200, 0, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			scan, pruned, err := pr.Query(c.plo, c.phi, c.vlo, c.vhi)
			if err != nil {
				t.Fatalf("Query: %v", err)
			}
			seen := map[string]bool{}
			for _, id := range pruned { // 被裁者：至少一维无交集
				seen[id] = true
				b := boxOf(id)
				if !(b[1] <= c.plo || b[0] >= c.phi) && !(b[3] < c.vlo || b[2] >= c.vhi) {
					t.Fatalf("%s pruned but bbox intersects both dims", id)
				}
			}
			for _, id := range scan { // 扫描者：两维都相交
				seen[id] = true
				b := boxOf(id)
				if !(b[1] > c.plo && b[0] < c.phi) || !(b[3] >= c.vlo && b[2] < c.vhi) {
					t.Fatalf("%s scanned but a dim is disjoint", id)
				}
			}
			if len(seen) != len(base) {
				t.Fatalf("partition set not partitioned: %v", seen)
			}
		})
	}
}

// TestRejectedOpsLeaveState 不变量4：每类拒绝零副作用、哨兵互异，之后仍可正常使用。
func TestRejectedOpsLeaveState(t *testing.T) {
	pr := buildBase(t)
	wantScan, wantPruned, _ := pr.Query(20, 50, 30, 60)
	badParts := []struct {
		id                 string
		lo, hi, minv, maxv int64
	}{
		{"X1", 5, 5, 0, 1}, {"X2", 5, 4, 0, 1}, {"X3", 5, 6, 9, 8},
		{"P2", 60, 70, 0, 1}, {"X4", 5, 15, 0, 1}, // 重复 id；范围重叠
	}
	for _, b := range badParts {
		if err := pr.AddPartition(b.id, b.lo, b.hi, b.minv, b.maxv); !errors.Is(err, api.ErrInvalidPartition) {
			t.Fatalf("bad partition %v: %v", b, err)
		}
	}
	badPred := [][4]int64{{50, 50, 30, 60}, {50, 40, 30, 60}, {20, 50, 60, 60}, {20, 50, 70, 60}}
	for _, b := range badPred {
		if _, _, err := pr.Query(b[0], b[1], b[2], b[3]); !errors.Is(err, api.ErrInvalidPredicate) {
			t.Fatalf("bad predicate %v: %v", b, err)
		}
	}
	for _, refs := range [][]string{{"NOPE"}, {"P0", "GHOST"}, {""}} {
		if _, _, err := pr.QueryRefs(refs, 20, 50, 30, 60); !errors.Is(err, api.ErrUnknownPartition) {
			t.Fatalf("unknown refs %v: %v", refs, err)
		}
	}
	if errors.Is(api.ErrInvalidPartition, api.ErrInvalidPredicate) ||
		errors.Is(api.ErrInvalidPredicate, api.ErrUnknownPartition) ||
		errors.Is(api.ErrInvalidPartition, api.ErrUnknownPartition) {
		t.Fatal("the three sentinel errors are not distinct")
	}
	gotScan, gotPruned, err := pr.Query(20, 50, 30, 60)
	if err != nil || !reflect.DeepEqual(gotScan, wantScan) || !reflect.DeepEqual(gotPruned, wantPruned) {
		t.Fatalf("state changed after rejections: %v %v %v", gotScan, gotPruned, err)
	}
	if err := pr.AddPartition("NEW", 60, 70, 1, 2); err != nil {
		t.Fatalf("table unusable after rejected ops: %v", err)
	}
}

// TestConcurrentQuery：并发 Query 与 SelfCheck，-race 干净且扫描集逐元素相同。
func TestConcurrentQuery(t *testing.T) {
	pr := buildBase(t)
	want, _, err := pr.Query(20, 50, 30, 60)
	if err != nil {
		t.Fatal(err)
	}
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				errs <- api.SelfCheck()
				return
			}
			got, _, qerr := pr.Query(20, 50, 30, 60)
			if qerr == nil && !reflect.DeepEqual(got, want) {
				qerr = fmt.Errorf("got %v want %v", got, want)
			}
			errs <- qerr
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
}

func TestSelfCheck(t *testing.T) {
	if err := api.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
