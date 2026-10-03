package triplesync

import (
	"math/rand"
	"testing"
)

func TestValidMergeRejectsDanglingAndCycle(t *testing.T) {
	if validMerge(Snapshot{1: {Parent: 9, Name: "x", Hash: "h"}}) {
		t.Fatal("dangling parent accepted")
	}
	if validMerge(Snapshot{
		1: {Parent: 2, Name: "a", Dir: true},
		2: {Parent: 1, Name: "b", Dir: true},
	}) {
		t.Fatal("cycle accepted")
	}
	if validMerge(Snapshot{
		1: {Parent: 0, Name: "a", Hash: "h"},
		2: {Parent: 0, Name: "a", Hash: "h"},
	}) {
		t.Fatal("duplicate names accepted")
	}
	if !validMerge(Snapshot{
		1: {Parent: 0, Name: "d", Dir: true},
		2: {Parent: 1, Name: "f", Hash: "h"},
	}) {
		t.Fatal("valid tree rejected")
	}
}

func TestRenameXChain(t *testing.T) {
	s := Snapshot{
		5: {Parent: 0, Name: "a", Hash: "h5"},
		4: {Parent: 0, Name: "a", Hash: "h4"},
		6: {Parent: 0, Name: "a.c5", Hash: "h6"},
		7: {Parent: 0, Name: "a.c5x", Hash: "h7"},
	}
	ren := resolveNameCollisions(s)
	if got := ren[5].Name; got != "a.c5xx" {
		t.Fatalf("rename = %q, want a.c5xx", got)
	}
}

func TestGeneratorProducesValidSides(t *testing.T) {
	for seed := int64(1); seed <= 500; seed++ {
		rng := rand.New(rand.NewSource(seed))
		base, local, remote := genWorld(rng)
		// The generator intentionally injects same-name siblings to exercise
		// ErrBadLocal/ErrBadRemote; every such side must fail validation for
		// exactly the duplicate-name reason, while the base is always valid.
		if !validateSnapshot(base) {
			t.Fatalf("seed %d generated invalid base", seed)
		}
		for _, s := range []Snapshot{local, remote} {
			if validateSnapshot(s) {
				continue
			}
			if !hasDuplicateSibling(s) {
				t.Fatalf("seed %d generated non-collision-invalid side: %v", seed, s)
			}
		}
	}
}

func hasDuplicateSibling(s Snapshot) bool {
	type k struct {
		p int64
		n string
	}
	seen := map[k]bool{}
	for _, e := range s {
		key := k{e.Parent, e.Name}
		if seen[key] {
			return true
		}
		seen[key] = true
	}
	return false
}
