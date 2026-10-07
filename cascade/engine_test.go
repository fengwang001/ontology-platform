package cascade

import (
	"reflect"
	"testing"
)

func set(ids ...string) map[string]bool {
	m := map[string]bool{}
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// 1. A link cycle must terminate cascade deletion and delete every cycle node.
func TestCycleTerminates(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"next": {OutRule: RuleCascade, InRule: RuleCascade},
	})
	store := NewMemoryStore()
	e := NewEngine(store, cfg)
	for _, id := range []string{"a", "b", "c"} {
		store.AddObject(id)
	}
	for _, l := range []Link{
		{ID: "l1", Type: "next", Src: "a", Dst: "b"},
		{ID: "l2", Type: "next", Src: "b", Dst: "c"},
		{ID: "l3", Type: "next", Src: "c", Dst: "a"},
	} {
		if err := e.AddLink(l); err != nil {
			t.Fatal(err)
		}
	}
	res, err := e.Delete("a")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !reflect.DeepEqual(res.Deleted, []string{"a", "b", "c"}) {
		t.Fatalf("deleted = %v", res.Deleted)
	}
	if len(store.snapshot().objects) != 0 || len(store.snapshot().links) != 0 {
		t.Fatalf("graph not empty: %#v %#v", store.snapshot().objects, store.snapshot().links)
	}
	// Cycle nodes are re-encountered via already-scheduled probes; no repeat work.
	if got := res.Plan.DedupProbes; got > 10 {
		t.Fatalf("dedup probes = %d, want small constant", got)
	}
}

// 2. Mixed rules on one object: one effective reject makes the whole
// request fail and restores state exactly.
func TestRestrictRollsEverythingBack(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"casc":  {OutRule: RuleCascade, InRule: RuleCascade},
		"keep":  {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
		"block": {OutRule: RuleSetNull, InRule: RuleRestrict},
	})
	build := func() (*Engine, *MemoryStore) {
		store := NewMemoryStore()
		e := NewEngine(store, cfg)
		for _, id := range []string{"a", "b", "c", "d"} {
			store.AddObject(id)
		}
		must(t, e.AddLink(Link{ID: "x", Type: "casc", Src: "a", Dst: "b"}))
		must(t, e.AddLink(Link{ID: "y", Type: "casc", Src: "b", Dst: "c"}))
		must(t, e.AddLink(Link{ID: "z", Type: "block", Src: "d", Dst: "b"}))
		return e, store
	}

	e, store := build()
	before := store.snapshot()
	_, err := e.Delete("a")
	if err == nil || err.(*CascadeError).Code != ErrRestricted {
		t.Fatalf("want restrict, got %v", err)
	}
	assertSnapshotEqual(t, before, store.snapshot())

	// Independent rule outcomes: removing the restrict link lets the same
	// cascade path succeed, proving the other rules were processed normally.
	store.DeleteLinks(set("z"))
	if _, err := e.Delete("a"); err != nil {
		t.Fatalf("delete after lift: %v", err)
	}
	if len(store.snapshot().objects) != 1 || !store.HasObject("d") {
		t.Fatalf("only d should survive, got %#v", store.snapshot().objects)
	}
}

// 3. Multi-round orphan chain: each cleanup exposes the next orphan.
func TestMultiRoundOrphans(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"owned": {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
		"casc":  {OutRule: RuleCascade, InRule: RuleCascade},
	})
	store := NewMemoryStore()
	e := NewEngine(store, cfg)
	for _, id := range []string{"root", "a", "b", "c", "free"} {
		store.AddObject(id)
	}
	// root ->(casc) a ; a keeps b ; b keeps c, three orphan rounds.
	must(t, e.AddLink(Link{ID: "l0", Type: "casc", Src: "root", Dst: "a"}))
	must(t, e.AddLink(Link{ID: "l1", Type: "owned", Src: "a", Dst: "b"}))
	must(t, e.AddLink(Link{ID: "l2", Type: "owned", Src: "b", Dst: "c"}))
	res, err := e.Delete("root")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a", "b", "c", "root"}
	if !reflect.DeepEqual(res.Deleted, want) {
		t.Fatalf("deleted = %v want %v", res.Deleted, want)
	}
	if !store.HasObject("free") {
		t.Fatal("unrelated object was removed")
	}
	kinds := map[string]int{}
	for _, s := range res.Plan.Steps {
		kinds[s.Kind]++
	}
	if kinds[stepOrphan] != 2 {
		t.Fatalf("orphan steps = %d, want 2", kinds[stepOrphan])
	}
}

// 4. Cross-check order independence: run the same request with planners
// driven by different internal orderings and compare final plans.
func TestOrderIndependence(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"owned": {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
		"casc":  {OutRule: RuleCascade, InRule: RuleSetNull},
		"null":  {OutRule: RuleSetNull, InRule: RuleCascade},
	})
	store := NewMemoryStore()
	for _, id := range []string{"r", "a", "b", "d", "e", "f"} {
		store.AddObject(id)
	}
	links := []Link{
		{ID: "1", Type: "casc", Src: "r", Dst: "a"},
		{ID: "2", Type: "owned", Src: "a", Dst: "b"},
		{ID: "3", Type: "owned", Src: "d", Dst: "b"},
		{ID: "4", Type: "owned", Src: "b", Dst: "e"},
		{ID: "5", Type: "null", Src: "f", Dst: "a"},
	}
	for _, l := range links {
		store.links[l.ID] = l
	}
	snap := store.snapshot()

	p1, err := plan(cfg, snap, "r")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := plan(cfg, shuffled(snap, t, 1), "r")
	if err != nil {
		t.Fatal(err)
	}
	p3, err := plan(cfg, shuffled(snap, t, 2), "r")
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range []*Plan{p2, p3} {
		if !reflect.DeepEqual(p1.Deleted, p.Deleted) || !reflect.DeepEqual(p1.RemovedLinks, p.RemovedLinks) {
			t.Fatalf("ordering %d differs:\n%#v\n%#v", i, p1, p)
		}
	}
}

// 5. Error precedence is fixed and mutually exclusive.
func TestErrorPrecedence(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"block": {OutRule: RuleRestrict, InRule: RuleRestrict},
	})
	store := NewMemoryStore()
	store.AddObject("a")
	e := NewEngine(store, cfg)

	if _, err := e.Delete("ghost"); err.(*CascadeError).Code != ErrObjectNotFound {
		t.Fatalf("want not found, got %v", err)
	}

	store.AddObject("b")
	must(t, e.AddLink(Link{ID: "x", Type: "block", Src: "a", Dst: "b"}))
	// undefined type incident to a plus an effective restrict: restrict wins.
	store.mu.Lock()
	store.links["u"] = Link{ID: "u", Type: "mystery", Src: "a", Dst: "b"}
	store.mu.Unlock()
	if _, err := e.Delete("a"); err.(*CascadeError).Code != ErrRestricted {
		t.Fatalf("want restrict, got %v", err)
	}

	store.DeleteLinks(set("x"))
	if _, err := e.Delete("a"); err.(*CascadeError).Code != ErrUndefinedLinkType {
		t.Fatalf("want undefined, got %v", err)
	}
}

func TestLogContainsInputPlanAndRules(t *testing.T) {
	cfg := NewConfig(map[string]LinkType{
		"owned": {OutRule: RuleSetNull, InRule: RuleSetNull, KeepAlive: true},
	})
	store := NewMemoryStore()
	e := NewEngine(store, cfg)
	store.AddObject("a")
	store.AddObject("b")
	must(t, e.AddLink(Link{ID: "l", Type: "owned", Src: "a", Dst: "b"}))
	if _, err := e.Delete("a"); err != nil {
		t.Fatal(err)
	}
	log := e.Log()
	if len(log) != 1 || log[0].Root != "a" || !reflect.DeepEqual(log[0].Deleted, []string{"a", "b"}) {
		t.Fatalf("bad log: %+v", log)
	}
	if len(log[0].Steps) == 0 || log[0].Steps[0].Object != "a" {
		t.Fatalf("steps missing: %+v", log[0].Steps)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func assertSnapshotEqual(t *testing.T, want, got snapshot) {
	t.Helper()
	if !reflect.DeepEqual(want.objects, got.objects) {
		t.Fatalf("objects changed: %v -> %v", want.objects, got.objects)
	}
	wantLinks := map[string]Link{}
	for _, l := range want.links {
		wantLinks[l.ID] = l
	}
	gotLinks := map[string]Link{}
	for _, l := range got.links {
		gotLinks[l.ID] = l
	}
	if !reflect.DeepEqual(wantLinks, gotLinks) {
		t.Fatalf("links changed: %v -> %v", wantLinks, gotLinks)
	}
}

func shuffled(snap snapshot, t *testing.T, seed int64) snapshot {
	t.Helper()
	ls := append([]Link(nil), snap.links...)
	// deterministic reverse/rotations, all distinct from sorted order
	for i, j := 0, len(ls)-1; i < j; i, j = i+1, j-1 {
		ls[i], ls[j] = ls[j], ls[i]
	}
	shift := int(seed) % len(ls)
	ls = append(ls[shift:], ls[:shift]...)
	return snapshot{objects: snap.objects, links: ls}
}
