package ontology

import "testing"

const migrationVnodes = 200

// Adding an 11th node to a 10-node ring must move a fraction of keys in the
// theoretical band around 1/11.
func TestAddNodeMigrationBound(t *testing.T) {
	keys := generateKeys(testKeyCount)

	ring := New()
	for i := 0; i < 10; i++ {
		if err := ring.Add(nodeName(i), migrationVnodes); err != nil {
			t.Fatalf("add node %d: %v", i, err)
		}
	}
	before := locateAll(ring, keys)

	if err := ring.Add(nodeName(10), migrationVnodes); err != nil {
		t.Fatalf("add 11th node: %v", err)
	}
	after := locateAll(ring, keys)

	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	frac := float64(moved) / float64(len(keys))
	lo, hi := 1.0/22.0, 3.0/22.0
	if frac < lo || frac > hi {
		t.Fatalf("moved fraction %.5f outside [%v, %v] (moved=%d)",
			frac, lo, hi, moved)
	}

	// Every moved key must now belong to the newly added node: adding a
	// node never migrates keys between two pre-existing nodes.
	for i := range keys {
		if before[i] != after[i] && after[i] != nodeName(10) {
			t.Fatalf("key %d migrated %q -> %q, not to the new node",
				i, before[i], after[i])
		}
	}
}

// Removing a node must reassign only keys it owned; every other key keeps its
// owner one by one. This is the defining property naive hash(key)%n fails.
func TestRemoveNodeOnlyItsKeysMove(t *testing.T) {
	keys := generateKeys(testKeyCount)

	ring := New()
	for i := 0; i < 10; i++ {
		if err := ring.Add(nodeName(i), migrationVnodes); err != nil {
			t.Fatalf("add node %d: %v", i, err)
		}
	}
	before := locateAll(ring, keys)

	victim := nodeName(4)
	if err := ring.Remove(victim); err != nil {
		t.Fatalf("remove %q: %v", victim, err)
	}
	after := locateAll(ring, keys)

	for i, key := range keys {
		switch before[i] {
		case victim:
			if after[i] == victim {
				t.Fatalf("key %q still on removed node %q", key, victim)
			}
			if !ring.Contains(after[i]) {
				t.Fatalf("key %q reassigned to absent node %q", key, after[i])
			}
		default:
			if after[i] != before[i] {
				t.Fatalf("key %q owned by unaffected node changed %q -> %q",
					key, before[i], after[i])
			}
		}
	}
}
