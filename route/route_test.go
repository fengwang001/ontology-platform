package route_test

import (
	"reflect"
	"testing"

	"ontology/route"
	"ontology/shard"
)

type evt struct {
	k string
	b int
}

// canonical is the eight-event S=2,T=3 sequence from NOTES.
var canonical = []evt{
	{"a", 0}, {"a", 0}, {"a", 0}, {"b", 1}, {"a", 0}, {"a", 0}, {"c", 1}, {"a", 0}}

// TestCanonicalEightSteps pins the derivation: the shard each event lands
// on, whether it triggers migration, and cnt after the event (ascending).
func TestCanonicalEightSteps(t *testing.T) {
	want := []struct {
		sh  int
		mig bool
		cnt []int64
	}{
		{0, false, []int64{1, 0}}, {0, false, []int64{2, 0}},
		{0, true, []int64{3, 0, 0}}, {1, false, []int64{3, 1, 0}},
		{2, false, []int64{3, 1, 1}}, {2, false, []int64{3, 1, 2}},
		{1, false, []int64{3, 2, 2}}, {2, false, []int64{3, 2, 3}},
	}
	r := route.NewRouter(shard.New(2, 3))
	for i, e := range canonical {
		o := r.Apply(e.k, e.b)
		if o.Shard != want[i].sh || o.Migrated != want[i].mig ||
			!reflect.DeepEqual(r.Snapshot(), want[i].cnt) {
			t.Fatalf("step %d got shard=%d mig=%v cnt=%v, want %d %v %v",
				i+1, o.Shard, o.Migrated, r.Snapshot(), want[i].sh, want[i].mig, want[i].cnt)
		}
	}
}

// TestMigrationAtomicity verifies the trigger event is counted on the base
// shard and the dedicated shard starts at zero; only later events use it.
func TestMigrationAtomicity(t *testing.T) {
	cases := []struct {
		name             string
		S, T, base, ded  int
		dedShard, trigAt int
	}{
		{"T3", 2, 3, 3, 2, 2, 3},
		{"T1", 2, 1, 1, 3, 2, 1},
	}
	for _, c := range cases {
		r := route.NewRouter(shard.New(c.S, c.T))
		for step := 1; step <= c.trigAt+c.ded; step++ { // trigger event + `ded` later events
			o := r.Apply("a", 0)
			if o.Migrated != (step == c.trigAt) {
				t.Fatalf("%s step %d mig=%v", c.name, step, o.Migrated)
			}
			if o.Migrated && r.Snapshot()[c.dedShard] != 0 { // trigger instant: dedicated still zero
				t.Fatalf("%s: trigger event was double-counted on the dedicated shard", c.name)
			}
		}
		g := r.Snapshot()
		if g[0] != int64(c.base) || g[c.dedShard] != int64(c.ded) {
			t.Fatalf("%s base=%d ded=%d, want %d/%d", c.name, g[0], g[c.dedShard], c.base, c.ded)
		}
		if sh, ok := r.Dedicated("a"); !ok || sh != c.dedShard || !r.IsHot("a") {
			t.Fatalf("%s dedicated=(%d,%v)", c.name, sh, ok)
		}
	}
}

// TestLookupChecksConstant proves (via a constant/not-constant verdict,
// never the raw counter) that deciding one non-hot key's status inspects a
// constant number of keys for m = 100,1000,10000 already-migrated keys:
// hash-map lookup rather than scanning the hot-key list.
func TestLookupChecksConstant(t *testing.T) {
	if !shard.LookupCostStaysConstant() {
		t.Fatal("migrated-status lookup cost grows with the number of hot keys")
	}
}
