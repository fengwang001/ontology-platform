package ontology

import "testing"

const (
	relocKeys   = 100000
	relocVnodes = 200
)

// Adding an 11th node to a 10-node ring must relocate a bounded share of
// keys. Theory says ~1/11; we require the measured ratio of changed
// ownerships over 100k fixed keys to land in [1/22, 3/22]. Every key is
// checked individually, not sampled.
func TestAddNodeRelocationBounded(t *testing.T) {
	keys := Keys(relocKeys)
	r := buildRing(t, nodeIDs(10), relocVnodes)
	before := owners(t, r, keys)

	if err := r.Add("node-10", relocVnodes); err != nil {
		t.Fatalf("Add node-10: %v", err)
	}
	after := owners(t, r, keys)

	moved := 0
	for i := range keys {
		if before[i] == after[i] {
			continue
		}
		moved++
		if after[i] != "node-10" {
			t.Fatalf("key %q moved %q -> %q; relocated keys must go to the new node",
				keys[i], before[i], after[i])
		}
	}
	ratio := float64(moved) / float64(len(keys))
	if lo, hi := 1.0/22, 3.0/22; ratio < lo || ratio > hi {
		t.Fatalf("relocation ratio %v (%d/%d) outside [%v, %v]; theory ~1/11",
			ratio, moved, len(keys), lo, hi)
	}
}

// Removing a node must only change ownership of the keys that node owned;
// every other key must keep its exact previous owner. This is the
// defining property of consistent hashing that hash(key)%n violates.
func TestRemoveNodeOnlyAffectsItsKeys(t *testing.T) {
	keys := Keys(relocKeys)
	r := buildRing(t, nodeIDs(10), relocVnodes)
	before := owners(t, r, keys)

	const victim = "node-3"
	if err := r.Remove(victim); err != nil {
		t.Fatalf("Remove %q: %v", victim, err)
	}
	after := owners(t, r, keys)

	affected := 0
	for i := range keys {
		if before[i] == victim {
			affected++
			if after[i] == victim {
				t.Fatalf("key %q still owned by removed node %q", keys[i], victim)
			}
			continue
		}
		if after[i] != before[i] {
			t.Fatalf("key %q changed owner %q -> %q after removing %q; only %q's keys may move",
				keys[i], before[i], after[i], victim, victim)
		}
	}
	if affected == 0 {
		t.Fatalf("no key was owned by %q; test is vacuous", victim)
	}
}
