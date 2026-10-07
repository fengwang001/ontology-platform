package temporal

import (
	"math/rand"
	"sort"
	"testing"
)

func TestPersistentTreapInsertDelete(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	present := map[string]bool{}

	var root *treapNode
	// 2000 random insert/deletes; keep every historical root and verify each
	// old root still answers membership exactly for the set at that time.
	type snapshot struct {
		root *treapNode
		set  map[string]bool
	}
	snaps := []snapshot{{nil, map[string]bool{}}}

	for i := 0; i < 2000; i++ {
		key := treapKey{linkType: "lt", other: ObjectID(itoa(rng.Intn(400)))}
		if rng.Intn(3) == 0 {
			root = treapDelete(root, key)
			delete(present, string(key.other))
		} else {
			root = treapInsert(root, key)
			present[string(key.other)] = true
		}
		if i%50 == 0 {
			cp := map[string]bool{}
			for k := range present {
				cp[k] = true
			}
			snaps = append(snaps, snapshot{root, cp})
		}
	}

	// Every captured historical root must still reflect its captured set even
	// though later inserts/deletes happened. This is the structural property
	// long-running traversals depend on.
	for _, snap := range snaps {
		for k, want := range snap.set {
			got := treapContains(snap.root, treapKey{linkType: "lt", other: ObjectID(k)})
			if got != want {
				t.Fatalf("historical root membership %s = %v want %v", k, got, want)
			}
		}
		var walked []string
		treapIter(snap.root, func(k treapKey) bool { walked = append(walked, string(k.other)); return true })
		sort.Strings(walked)
		var wantKeys []string
		for k := range snap.set {
			wantKeys = append(wantKeys, k)
		}
		sort.Strings(wantKeys)
		if len(walked) != len(wantKeys) {
			t.Fatalf("historical root walk len = %d want %d", len(walked), len(wantKeys))
		}
		for i := range walked {
			if walked[i] != wantKeys[i] {
				t.Fatalf("historical root walk order/content mismatch")
			}
		}
	}

	// Determinism: building the same sequence twice yields identical
	// membership/iteration (no random priorities).
	var a, b *treapNode
	for i := 0; i < 100; i++ {
		a = treapInsert(a, treapKey{linkType: "lt", other: ObjectID(itoa(i))})
		b = treapInsert(b, treapKey{linkType: "lt", other: ObjectID(itoa(i))})
	}
	for i := 0; i < 100; i++ {
		k := treapKey{linkType: "lt", other: ObjectID(itoa(i))}
		if treapContains(a, k) != treapContains(b, k) {
			t.Fatalf("nondeterministic treap build")
		}
	}
}

func TestPersistentTreapRootsIsolated(t *testing.T) {
	root := treapInsert(nil, treapKey{linkType: "l", other: "x"})
	root2 := treapInsert(root, treapKey{linkType: "l", other: "y"})
	if treapContains(root, treapKey{linkType: "l", other: "y"}) {
		t.Fatalf("old root must not observe the later insertion")
	}
	if !treapContains(root2, treapKey{linkType: "l", other: "y"}) {
		t.Fatalf("new root must contain later insertion")
	}
}
