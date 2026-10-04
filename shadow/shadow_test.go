package shadow

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"

	"ontology/delta"
	"ontology/doc"
)

func paths(es []delta.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Path
	}
	return out
}

func eqStr(a, b []string) bool {
	a = append([]string(nil), a...)
	b = append([]string(nil), b...)
	sort.Strings(a)
	sort.Strings(b)
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

func TestWorkedExample(t *testing.T) {
	svc, _ := NewService(10000)
	const dev = "d1"
	if err := svc.Create(dev); err != nil {
		t.Fatal(err)
	}

	r, err := svc.UpdateDesired(dev, doc.Obj(
		"a", doc.Obj("b", doc.IntLeaf(1), "c", doc.IntLeaf(2)),
		"d", doc.StringLeaf("x"),
	), 0)
	if err != nil || r.NoChange || r.Version != 1 {
		t.Fatalf("step1 %+v %v", r, err)
	}
	if got := paths(r.Upsert); !eqStr(got, []string{"a.b", "a.c", "d"}) || len(r.Remove) != 0 {
		t.Fatalf("step1 ups=%v rem=%v", got, r.Remove)
	}
	if r.Upsert[0].MV != 1 {
		t.Fatalf("mv=%d want 1", r.Upsert[0].MV)
	}

	r, err = svc.UpdateReported(dev, doc.Obj("a", doc.Obj("b", doc.IntLeaf(1)), "d", doc.StringLeaf("y")), 0)
	if err != nil || r.NoChange || r.Version != 2 {
		t.Fatalf("step2 %+v %v", r, err)
	}
	if !eqStr(r.Remove, []string{"a.b"}) || len(r.Upsert) != 0 {
		t.Fatalf("step2 ups=%v rem=%v", r.Upsert, r.Remove)
	}

	r, err = svc.UpdateDesired(dev, doc.Obj("a", doc.Obj("c", doc.NullNode()), "d", doc.StringLeaf("x")), 0)
	if err != nil || r.NoChange || r.Version != 3 {
		t.Fatalf("step3 %+v %v", r, err)
	}
	if !eqStr(r.Remove, []string{"a.c"}) || len(r.Upsert) != 0 {
		t.Fatalf("step3 ups=%v rem=%v", r.Upsert, r.Remove)
	}
	_, _, mv, _, _, _ := svc.Snapshot(dev)
	if mv["d"] != 1 {
		t.Fatalf("mv[d]=%d want 1 (unchanged leaf keeps mv)", mv["d"])
	}

	r, err = svc.UpdateDesired(dev, doc.Obj("a", doc.Obj("b", doc.Obj())), 0)
	if err != nil || r.NoChange || r.Version != 4 {
		t.Fatalf("step4 %+v %v", r, err)
	}
	if len(r.Upsert) != 0 || len(r.Remove) != 0 {
		t.Fatalf("step4 ups=%v rem=%v", r.Upsert, r.Remove)
	}

	r, err = svc.UpdateDesired(dev, doc.Obj("e", doc.Obj()), 0)
	if err != nil || !r.NoChange || r.Version != 4 {
		t.Fatalf("step5 %+v %v", r, err)
	}

	if _, err = svc.UpdateReported(dev, doc.Obj("d", doc.StringLeaf("x")), 3); !errors.Is(err, ErrVersion) {
		t.Fatalf("step6 expect=3 err=%v want ErrVersion", err)
	}
	r, err = svc.UpdateReported(dev, doc.Obj("d", doc.StringLeaf("x")), 4)
	if err != nil || r.NoChange || r.Version != 5 {
		t.Fatalf("step6 %+v %v", r, err)
	}
	if !eqStr(r.Remove, []string{"d"}) || len(r.Upsert) != 0 {
		t.Fatalf("step6 ups=%v rem=%v", r.Upsert, r.Remove)
	}
	all, _ := svc.GetDelta(dev)
	if len(all) != 0 {
		t.Fatalf("final delta=%v", all)
	}
}

func TestObjectLeafInterchange(t *testing.T) {
	svc, _ := NewService(10000)
	svc.Create("x")
	svc.UpdateDesired("x", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1), "y", doc.IntLeaf(2))), 0)
	svc.UpdateReported("x", doc.Obj("m", doc.IntLeaf(5)), 0)
	all, _ := svc.GetDelta("x")
	if !eqStr(paths(all), []string{"m.x", "m.y"}) {
		t.Fatalf("delta=%v", all)
	}
	r, err := svc.UpdateReported("x", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1))), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !eqStr(r.Remove, []string{"m.x"}) || len(r.Upsert) != 0 {
		t.Fatalf("r=%+v", r)
	}
	all, _ = svc.GetDelta("x")
	if !eqStr(paths(all), []string{"m.y"}) {
		t.Fatalf("delta=%v", all)
	}

	svc.Create("y")
	svc.UpdateDesired("y", doc.Obj("m", doc.IntLeaf(7)), 0)
	svc.UpdateReported("y", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1))), 0)
	all, _ = svc.GetDelta("y")
	if len(all) != 1 || all[0].Path != "m" || all[0].Val.Kind != doc.Int || all[0].Val.I != 7 {
		t.Fatalf("delta=%v", all)
	}
}

func TestSubtreeReplaceMV(t *testing.T) {
	svc, _ := NewService(10000)
	svc.Create("z")
	r, _ := svc.UpdateDesired("z", doc.Obj("a", doc.Obj("b", doc.IntLeaf(1))), 0)
	if r.Version != 1 {
		t.Fatal("ver")
	}
	r, _ = svc.UpdateDesired("z", doc.Obj("a", doc.Obj("b", doc.IntLeaf(1), "c", doc.IntLeaf(2))), 0)
	if r.Version != 2 {
		t.Fatal("ver2")
	}
	_, _, mv, _, _, _ := svc.Snapshot("z")
	if mv["a.b"] != 1 || mv["a.c"] != 2 {
		t.Fatalf("mv=%v", mv)
	}
	r, _ = svc.UpdateDesired("z", doc.Obj("a", doc.IntLeaf(9)), 0)
	if r.Version != 3 {
		t.Fatalf("after leaf replace ver=%d", r.Version)
	}
	if got := mvFor(svc, "z", "a"); got != 3 {
		t.Fatalf("after leaf replace mv[a]=%d want 3", got)
	}
	r, _ = svc.UpdateDesired("z", doc.Obj("a", doc.Obj("b", doc.IntLeaf(1))), 0)
	if r.Version != 4 {
		t.Fatalf("after object replace ver=%d", r.Version)
	}
	if got := mvFor(svc, "z", "a.b"); got != 4 {
		t.Fatalf("after object replace mv[a.b]=%d want 4", got)
	}
}

func mvFor(svc *Service, dev, path string) int64 {
	_, _, mv, _, _, _ := svc.Snapshot(dev)
	return mv[path]
}

func TestRejectionOrdering(t *testing.T) {
	svc, _ := NewService(1)
	if _, err := svc.UpdateDesired("", doc.Obj("k", doc.IntLeaf(1)), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty dev err=%v", err)
	}
	if _, err := svc.UpdateDesired("nope", doc.Obj(), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty patch on missing dev err=%v want ErrInvalid", err)
	}
	if _, err := svc.UpdateDesired("nope", doc.Obj("a.b", doc.IntLeaf(1)), 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad key err=%v", err)
	}
	deep := doc.Obj("a", doc.Obj("b", doc.Obj("c", doc.Obj("d", doc.Obj("e", doc.IntLeaf(1))))))
	if _, err := svc.UpdateDesired("nope", deep, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deep patch err=%v", err)
	}
	if _, err := svc.UpdateDesired("ghost", doc.Obj("k", doc.IntLeaf(1)), 99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("notfound err=%v", err)
	}
	svc.Create("c")
	if err := svc.Create("c"); !errors.Is(err, ErrExists) {
		t.Fatalf("dup create err=%v", err)
	}
	if _, err := svc.UpdateDesired("c", doc.Obj("a", doc.IntLeaf(1), "b", doc.IntLeaf(2)), 99); !errors.Is(err, ErrVersion) {
		t.Fatalf("version err=%v", err)
	}
	if _, err := svc.UpdateDesired("c", doc.Obj("a", doc.IntLeaf(1), "b", doc.IntLeaf(2)), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("toolarge err=%v", err)
	}
	ver, _ := svc.Version("c")
	if ver != 0 {
		t.Fatalf("state changed on rejection ver=%d", ver)
	}
}

func TestLeafLimitExact(t *testing.T) {
	svc, _ := NewService(2)
	svc.Create("c")
	if r, err := svc.UpdateDesired("c", doc.Obj("a", doc.IntLeaf(1), "b", doc.IntLeaf(2)), 0); err != nil || r.Version != 1 {
		t.Fatalf("exact L err=%v", err)
	}
	if _, err := svc.UpdateDesired("c", doc.Obj("c", doc.IntLeaf(3)), 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("L+1 err=%v", err)
	}
}

func TestDepthFourAndFive(t *testing.T) {
	svc, _ := NewService(100)
	svc.Create("c")
	ok4 := doc.Obj("a", doc.Obj("b", doc.Obj("c", doc.Obj("d", doc.IntLeaf(1)))))
	if _, err := svc.UpdateDesired("c", ok4, 0); err != nil {
		t.Fatalf("4-seg err=%v", err)
	}
	bad5 := doc.Obj("a", doc.Obj("b", doc.Obj("c", doc.Obj("d", doc.Obj("e", doc.IntLeaf(1))))))
	if _, err := svc.UpdateDesired("c", bad5, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("5-seg err=%v", err)
	}
	deepNull := doc.Obj("a", doc.Obj("b", doc.Obj("c", doc.Obj("d", doc.Obj("e", doc.NullNode())))))
	if _, err := svc.UpdateDesired("c", deepNull, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("deep-null err=%v", err)
	}
}

func TestExpectSuccess(t *testing.T) {
	svc, _ := NewService(100)
	svc.Create("c")
	r, _ := svc.UpdateDesired("c", doc.Obj("a", doc.IntLeaf(1)), 0)
	if r.Version != 1 {
		t.Fatal("ver")
	}
	if _, err := svc.UpdateDesired("c", doc.Obj("b", doc.IntLeaf(2)), 1); err != nil {
		t.Fatalf("expect=1 err=%v", err)
	}
}

func TestConcurrentExpect(t *testing.T) {
	svc, _ := NewService(10000)
	svc.Create("c")
	svc.UpdateDesired("c", doc.Obj("seed", doc.IntLeaf(0)), 0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, nochange := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := svc.UpdateDesired("c", doc.Obj("k", doc.IntLeaf(int64(i))), 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case errors.Is(err, ErrVersion):
			case err != nil:
				t.Errorf("unexpected err %v", err)
			case r.NoChange:
				nochange++
			default:
				success++
			}
		}(i)
	}
	wg.Wait()
	if success != 1 || nochange != 0 {
		t.Fatalf("success=%d nochange=%d (want exactly 1 increment)", success, nochange)
	}
	ver, _ := svc.Version("c")
	if ver != 2 {
		t.Fatalf("ver=%d want 2", ver)
	}
}

func TestNullDeleteMissingNoChange(t *testing.T) {
	svc, _ := NewService(100)
	svc.Create("c")
	r, err := svc.UpdateDesired("c", doc.Obj("ghost", doc.NullNode()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if !r.NoChange || r.Version != 0 || r.Touched != 0 {
		t.Fatalf("r=%+v", r)
	}
}

func TestReportedEmptyObjectUnhidesDesiredLeaves(t *testing.T) {
	svc, _ := NewService(100)
	svc.Create("c")
	// Desired subtree exists, reported leaf blocks all its leaves.
	svc.UpdateDesired("c", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1), "y", doc.IntLeaf(2))), 0)
	all, _ := svc.GetDelta("c")
	if !eqStr(paths(all), []string{"m.x", "m.y"}) {
		t.Fatalf("delta after desired=%v", all)
	}
	// Reporting a blocking leaf m=5 keeps both leaves in the delta; content
	// and mv are unchanged so no movement.
	r, err := svc.UpdateReported("c", doc.Obj("m", doc.IntLeaf(5)), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Upsert) != 0 || len(r.Remove) != 0 {
		t.Fatalf("blocking leaf produced movement up=%v rm=%v", r.Upsert, r.Remove)
	}
	// Empty-object patch on the reported leaf deletes m; delta content is
	// unchanged (m.x,m.y still differ), so lists stay empty but ver grows.
	r, err = svc.UpdateReported("c", doc.Obj("m", doc.Obj()), 0)
	if err != nil || r.NoChange {
		t.Fatalf("empty-object delete err=%v r=%+v", err, r)
	}
	if len(r.Upsert) != 0 || len(r.Remove) != 0 {
		t.Fatalf("delete blocking leaf produced movement up=%v rm=%v", r.Upsert, r.Remove)
	}
	if !eqStr(paths(all), []string{"m.x", "m.y"}) {
		t.Fatalf("delta=%v", all)
	}
	// Reporting the whole object removes both.
	r, _ = svc.UpdateReported("c", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1), "y", doc.IntLeaf(2))), 0)
	if !eqStr(r.Remove, []string{"m.x", "m.y"}) {
		t.Fatalf("remove=%v", r.Remove)
	}
}

func TestReportedLeafBecomesObjectRemovesEntry(t *testing.T) {
	svc, _ := NewService(100)
	svc.Create("c")
	// Desired leaf m=7, reported object m.x=1 -> m is in delta.
	svc.UpdateDesired("c", doc.Obj("m", doc.IntLeaf(7)), 0)
	svc.UpdateReported("c", doc.Obj("m", doc.Obj("x", doc.IntLeaf(1))), 0)
	all, _ := svc.GetDelta("c")
	if len(all) != 1 || all[0].Path != "m" {
		t.Fatalf("delta=%v", all)
	}
	// Reported m.x changes: m itself is unaffected (path differs) -> no change.
	r, err := svc.UpdateReported("c", doc.Obj("m", doc.Obj("x", doc.IntLeaf(2))), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Upsert) != 0 || len(r.Remove) != 0 {
		t.Fatalf("unrelated reported change moved delta: %+v", r)
	}
	// Reporting m=7 matches -> m leaves delta.
	r, _ = svc.UpdateReported("c", doc.Obj("m", doc.IntLeaf(7)), 0)
	if !eqStr(r.Remove, []string{"m"}) || len(r.Upsert) != 0 {
		t.Fatalf("remove=%v ups=%v", r.Remove, r.Upsert)
	}
}

func TestTouchedSameAcrossSizes(t *testing.T) {
	run := func(fillers int) int {
		svc, _ := NewService(10000)
		dev := fmt.Sprintf("dev%d", fillers)
		svc.Create(dev)
		fill := doc.NewObject()
		for i := 0; i < fillers; i++ {
			fill.Kids[fmt.Sprintf("f%05d", i)] = doc.IntLeaf(int64(i))
		}
		if _, err := svc.UpdateDesired(dev, fill, 0); err != nil {
			t.Fatal(err)
		}
		patch := doc.Obj(
			"z", doc.Obj("p", doc.Obj("q", doc.IntLeaf(7))),
			"zz", doc.Obj(),
		)
		r, err := svc.UpdateDesired(dev, patch, 0)
		if err != nil {
			t.Fatal(err)
		}
		return r.Touched
	}
	t100 := run(100)
	t9000 := run(9000)
	if t100 != t9000 || t100 == 0 {
		t.Fatalf("touched 100=%d 9000=%d", t100, t9000)
	}
}
