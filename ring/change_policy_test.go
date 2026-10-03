package ring

import (
	"testing"

	"ontology/change"
)

func TestDeleteReplicatesAndBlocksOlderPut(t *testing.T) {
	r, err := New(4, []int{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	writeOK(t, r, 1, "k", change.Put{Value: 1}, 1)
	writeOK(t, r, 2, "k", change.Del, 5)
	drainAll(t, r)
	for site := 1; site <= 4; site++ {
		entry, ok, err := r.Get(site, []byte("k"))
		if err != nil || !ok || !entry.Delete || entry.Triple.TS != 5 {
			t.Fatalf("site %d entry=%+v ok=%v err=%v", site, entry, ok, err)
		}
	}

	writeOK(t, r, 1, "k", change.Put{Value: 9}, 4)
	if outcome := deliverOK(t, r, 1, 2); outcome != DeliverLost {
		t.Fatalf("old put at site 2 = %v, want lost", outcome)
	}
	if outcome := deliverOK(t, r, 1, 4); outcome != DeliverLost {
		t.Fatalf("old put at fresh neighbor = %v, want lost", outcome)
	}
	drainAll(t, r)
	for site := 1; site <= 4; site++ {
		entry, ok, err := r.Get(site, []byte("k"))
		if err != nil || !ok || !entry.Delete {
			t.Fatalf("site %d lost tombstone: entry=%+v ok=%v err=%v", site, entry, ok, err)
		}
	}
}
