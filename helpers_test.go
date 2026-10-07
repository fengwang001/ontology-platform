package ontology

import "testing"

// setupEnv builds the standard two-type, one-link, one-view fixture:
// type A (grouping prop "ta") and type B (grouping prop "tb") connected by
// link relation "L", with view "v" over it.
func setupEnv(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.DefineObjectType("A", "ta", "ta"))
	must(e.DefineObjectType("B", "tb", "tb"))
	must(e.DefineLinkType("L", "A", "B"))
	must(e.NewView("v", "L"))
	return e
}

func mustDo(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, e *Engine, obj, typ, prop, wall string) Event {
	t.Helper()
	ev, err := e.Write(obj, typ, prop, MustWall(wall))
	mustDo(t, err)
	return ev
}

func mustLink(t *testing.T, e *Engine, left, right string) Event {
	t.Helper()
	ev, err := e.Link("L", left, "A", right, "B")
	mustDo(t, err)
	return ev
}

// checkViewInvariants verifies the structural invariants of the view: every
// object appears in at most one group, group keys are sorted, and every
// group is strictly ordered by the deterministic tie-break rule.
func checkViewInvariants(t *testing.T, e *Engine, viewID string) {
	t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	v := e.views[viewID]
	seen := make(map[string]string)
	for i := 1; i < len(v.keys); i++ {
		if v.keys[i-1] >= v.keys[i] {
			t.Fatalf("group keys not sorted: %v", v.keys)
		}
	}
	for _, key := range v.keys {
		g := v.groups[key]
		if g == nil || len(g.items) == 0 {
			t.Fatalf("empty group %q materialized", key)
		}
		for i, it := range g.items {
			if it.GroupKey != key {
				t.Fatalf("item %s in wrong group %q (has %q)", it.ObjID, key, it.GroupKey)
			}
			if prev, dup := seen[it.ObjID]; dup {
				t.Fatalf("object %s in two groups: %q and %q", it.ObjID, prev, key)
			}
			seen[it.ObjID] = key
			if i > 0 && !lessItem(g.items[i-1], it) {
				t.Fatalf("group %q not strictly ordered at %d", key, i)
			}
		}
	}
	// every placed objState must be found in exactly its recorded group
	for id, o := range v.objs {
		if !o.placed {
			continue
		}
		if seen[id] != o.item.GroupKey {
			t.Fatalf("objState %s placed in %q but found in %q", id, o.item.GroupKey, seen[id])
		}
	}
}

// groupSnapshot renders groups into a comparable canonical string map.
func groupSnapshot(groups []Group) map[string][]string {
	out := make(map[string][]string, len(groups))
	for _, g := range groups {
		for _, it := range g.Items {
			out[g.Key] = append(out[g.Key], it.ObjID+"@"+it.Normalized.Format("15:04:05"))
		}
	}
	return out
}

// flatten renders the full ordered result as "group/obj" pairs for exact
// order-sensitive comparison.
func flatten(groups []Group) []string {
	var out []string
	for _, g := range groups {
		for _, it := range g.Items {
			out = append(out, g.Key+"/"+it.ObjID)
		}
	}
	return out
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
