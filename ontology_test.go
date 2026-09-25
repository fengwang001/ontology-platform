package ontology_test

import (
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"ontology/agg"
	"ontology/audit"
	"ontology/change"
	"ontology/journal"
	"ontology/view"
)

func ins(ver uint64, id, g string, v float64) change.Change {
	return change.Change{Version: ver, Op: change.Insert, ID: id, HasGroup: true, Group: g, Value: v}
}
func del(ver uint64, id, g string, v float64) change.Change {
	return change.Change{Version: ver, Op: change.Delete, ID: id, HasGroup: true, Group: g, Value: v}
}

func TestAggregatorRetraction(t *testing.T) {
	cases := []struct {
		kind                     agg.Kind
		insertOK, deleteOK, need bool
	}{
		{agg.Count, true, true, false}, {agg.Sum, true, true, false},
		{agg.Min, true, false, true}, {agg.Max, true, false, true}, {agg.DistinctCount, true, false, true},
	}
	for _, tc := range cases {
		a := agg.New(tc.kind)
		a.Insert(1)
		if a.NeedsMembers() != tc.need || a.Delete(1) != tc.need {
			t.Fatalf("%s retraction policy", tc.kind)
		}
	}
	v := view.New()
	for i := range 100000 {
		if err := v.Submit(ins(uint64(i+1), fmt.Sprintf("r%d", i), "g", float64(100000-i))); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4997; i++ {
		if err := v.Submit(del(uint64(100001+i), fmt.Sprintf("r%d", i+3), "g", float64(100000-i-3))); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range []string{"r0", "r1", "r2"} {
		if err := v.Submit(del(uint64(150000+i), id, "g", float64(100000-i))); err != nil {
			t.Fatal(err)
		}
	}
	m := v.Metrics()
	if v.RecomputeCount(agg.Min) != 3 || v.RecomputeCount(agg.Count) != 0 ||
		v.RecomputeCount(agg.Sum) != 0 || v.MembersScannedFor(agg.Min) > 100000*3 {
		t.Fatalf("metrics=%+v", m)
	}
}

func TestAuditVersionsBoundaries(t *testing.T) {
	v, cs, rng := view.New(), []change.Change{}, rand.New(rand.NewPCG(1, 2))
	records := map[string]struct {
		g string
		v float64
	}{}
	for n := uint64(1); n <= 50000; n++ {
		id := fmt.Sprintf("r%d", rng.IntN(12000))
		g := fmt.Sprintf("g%d", rng.IntN(50))
		c := ins(n, id, g, math.Float64frombits(rng.Uint64()&0x7fffffffffffffff))
		if old, ok := records[id]; ok && rng.IntN(3) == 0 {
			c = del(n, id, old.g, old.v)
			delete(records, id)
		} else if old, ok := records[id]; ok {
			c.Op, c.Group, c.Value, c.NewHasGrp, c.NewGroup, c.NewValue = change.Update, old.g, old.v, true, g, c.Value
			records[id] = struct {
				g string
				v float64
			}{g, c.NewValue}
		} else {
			records[id] = struct {
				g string
				v float64
			}{g, c.Value}
		}
		if err := v.Submit(c); err != nil {
			t.Fatal(n, err)
		}
		cs = append(cs, c)
		if n%1000 == 0 && audit.Check(v, cs) != nil {
			t.Fatal(audit.Check(v, cs))
		}
	}
	before := v.Snapshot()
	last := cs[len(cs)-1]
	if err := v.Submit(last); err != nil || !reflect.DeepEqual(before, v.Snapshot()) || v.Version() != 50000 {
		t.Fatal("duplicate not idempotent")
	}
	if !errors.Is(v.Submit(del(1, "x", "g", 1)), view.ErrVersionBack) {
		t.Fatal("old version")
	}
	missing := ins(50001, "x", "", 1)
	missing.HasGroup = false
	nan := ins(50002, "y", "g", math.NaN())
	if v.Submit(missing) == nil || v.Submit(nan) == nil || v.Rejected() < 2 {
		t.Fatal("invalid accepted")
	}
}

func TestTruncationRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "j")
	j, _ := journal.Create(path)
	for i := range 200 {
		if err := j.Append(ins(uint64(i+1), fmt.Sprintf("r%d", i), "g", float64(i))); err != nil {
			t.Fatal(err)
		}
	}
	j.Close()
	full, _ := os.ReadFile(path)
	classes := map[error]int{}
	for n := 1; n < len(full); n++ {
		cs, _ := journal.Parse(full[:n])
		cls := journal.ClassifyTruncation(full[:n], full)
		switch {
		case errors.Is(cls, journal.ErrIncompleteHeader):
			classes[journal.ErrIncompleteHeader]++
		case errors.Is(cls, journal.ErrIncompleteLength):
			classes[journal.ErrIncompleteLength]++
		case errors.Is(cls, journal.ErrIncompleteRecord):
			classes[journal.ErrIncompleteRecord]++
		case errors.Is(cls, journal.ErrCRC):
			classes[journal.ErrCRC]++
		default:
			t.Fatalf("byte %d unclassified: %v", n, cls)
		}
		rv := view.New()
		for _, c := range cs {
			if err := rv.Submit(c); err != nil {
				t.Fatal(n, err)
			}
		}
		want, _ := audit.Recompute(cs)
		if err := audit.Compare(rv, want); err != nil {
			t.Fatal(n, err)
		}
	}
	if classes[journal.ErrIncompleteHeader] != journal.HeaderSize-1 || classes[journal.ErrCRC] != 0 {
		t.Fatalf("classes=%+v len=%d", classes, len(full))
	}
	if classes[journal.ErrIncompleteLength] == 0 || classes[journal.ErrIncompleteRecord] == 0 {
		t.Fatalf("classes=%+v", classes)
	}
}

func TestCrashAndConcurrency(t *testing.T) {
	cases := []view.CrashPoint{view.AfterApply, view.InRecompute, view.BeforeCommit}
	base := []change.Change{ins(1, "a", "g", 3), ins(2, "b", "g", 1)}
	for _, point := range cases {
		path := filepath.Join(t.TempDir(), "j")
		v, _ := view.Create(path)
		v.SetCrashPoint(point)
		_ = v.Submit(del(3, "b", "g", 1))
		v.Close()
		r, err := view.Recover(path)
		if err != nil {
			t.Fatal(err)
		}
		good := view.New()
		for _, c := range base {
			if err := good.Submit(c); err != nil {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(good.Snapshot(), r.Snapshot()) {
			t.Fatal(point)
		}
	}
	v, wg, versions := view.New(), sync.WaitGroup{}, make(chan uint64, 1200)
	for n := uint64(1); n <= 1200; n++ {
		versions <- n
	}
	close(versions)
	for w := 0; w < 12; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for n := range versions {
				id := fmt.Sprintf("r%d", n)
				if err := v.Submit(ins(n, id, "g", 1)); err != nil {
					t.Error(err)
				}
			}
		}(w)
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			default:
				_ = v.Snapshot()
			}
		}
	}()
	wg.Wait()
	close(done)
	if n, _ := v.Lookup("g", agg.Count); n != 1200 {
		t.Fatal(n)
	}
}
