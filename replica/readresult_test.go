package replica

import (
	"testing"

	"ontology/bus"
	"ontology/entry"
)

// Requirement 7: "backend says absent" and "never fetched" are two
// distinguishable read outcomes; the negative cache carries a
// version and a TTL.
func TestHoleVersusAbsentDistinguishable(t *testing.T) {
	f := newFixture(t, ttl, 0)
	if info := f.rep.Inspect("k"); info.State != entry.Hole || info != (KeyInfo{State: entry.Hole}) {
		t.Fatalf("unknown key = %+v", info)
	}
	res := mustRead(t, f.rep, "k")
	if res.State != Absent || res.Found {
		t.Fatalf("absent read = %+v", res)
	}
	info := f.rep.Inspect("k")
	if info.State != entry.Valid || info.Remaining <= 0 || info.Version.IsZero() {
		t.Fatalf("negative entry = %+v", info)
	}
	// A newer invalidation applies to the negative entry too.
	nv := f.be.alloc.Next()
	if err := f.rep.ApplyNotification(bus.Notification{Key: "k", Version: nv}); err != nil {
		t.Fatal(err)
	}
	if info := f.rep.Inspect("k"); info.State != entry.Stale || info.Version != nv {
		t.Fatalf("after invalidate = %+v", info)
	}
}
