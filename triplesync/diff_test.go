package triplesync

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func dumpSnap(b *bytes.Buffer, name string, s Snapshot) {
	ids := make([]int64, 0, len(s))
	for id := range s {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	fmt.Fprintf(b, "%s={", name)
	for _, id := range ids {
		e := s[id]
		kind := "f"
		if e.Dir {
			kind = "d"
		}
		fmt.Fprintf(b, "%d:%s(p=%d,n=%q,h=%q) ", id, kind, e.Parent, e.Name, e.Hash)
	}
	b.WriteString("}\n")
}

func runDifferential(t *testing.T, cases int, verbose bool) {
	t.Helper()
	var log bytes.Buffer
	mismatches := 0
	for seed := int64(1); seed <= int64(cases); seed++ {
		rng := rand.New(rand.NewSource(seed))
		base, local, remote := genWorld(rng)

		p, berr := NewPlanner(copySnap(base))
		var got Plan
		var gerr error
		if berr == nil {
			got, gerr = p.Plan(copySnap(local), copySnap(remote))
		} else {
			gerr = berr
		}
		want, rationale, nerr := naivePlan(copySnap(base), copySnap(local), copySnap(remote))

		fmt.Fprintf(&log, "==== seed=%d ====\n", seed)
		dumpSnap(&log, "B", base)
		dumpSnap(&log, "L", local)
		dumpSnap(&log, "R", remote)
		for _, line := range rationale {
			fmt.Fprintf(&log, "  %s\n", line)
		}

		if fmt.Sprint(gerr) != fmt.Sprint(nerr) || (gerr == nil && !reflect.DeepEqual(got, want)) {
			mismatches++
			fmt.Fprintf(&log, "  MISMATCH: got=(%v,%v) want=(%v,%v)\n", got, gerr, want, nerr)
			t.Errorf("seed %d mismatch:\ngot plan=%+v err=%v\nwant plan=%+v err=%v", seed, got, gerr, want, nerr)
		}
	}
	if verbose || mismatches > 0 {
		path := "/tmp/triplesync_diff.log"
		if err := os.WriteFile(path, log.Bytes(), 0o644); err != nil {
			t.Logf("write log: %v", err)
		} else {
			t.Logf("differential log written to %s", path)
		}
	}
}

func TestDifferential2000(t *testing.T) {
	runDifferential(t, 2000, testing.Verbose())
}

func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	base, local, remote := genWorld(rng)
	p1, _ := NewPlanner(copySnap(base))
	p2, _ := NewPlanner(copySnap(base))
	r1, e1 := p1.Plan(copySnap(local), copySnap(remote))
	r2, e2 := p2.Plan(copySnap(local), copySnap(remote))
	if !reflect.DeepEqual(r1, r2) || e1 != e2 {
		t.Fatalf("replay differs: %+v vs %+v", r1, r2)
	}
}

func TestConcurrentPlans(t *testing.T) {
	base := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
	}
	local := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h3"},
	}
	remote := Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h2"},
	}
	p, err := NewPlanner(base)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			plan, err := p.Plan(local, remote)
			if err != nil {
				t.Error(err)
				return
			}
			if len(plan.Conflicts) != 0 || len(plan.ToRemote) != 1 || plan.ToRemote[0].Kind != ActionSetHash {
				t.Errorf("bad plan: %+v", plan)
			}
		}()
	}
	wg.Wait()
}

func copySnap(s Snapshot) Snapshot {
	c := make(Snapshot, len(s))
	for k, v := range s {
		c[k] = v
	}
	return c
}
