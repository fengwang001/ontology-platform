package ontology

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// naiveDifference returns sorted A\B and B\A.
func naiveDifference(a, b map[uint64]struct{}) ([]uint64, []uint64) {
	var onlyA, onlyB []uint64
	for x := range a {
		if _, ok := b[x]; !ok {
			onlyA = append(onlyA, x)
		}
	}
	for x := range b {
		if _, ok := a[x]; !ok {
			onlyB = append(onlyB, x)
		}
	}
	slices.Sort(onlyA)
	slices.Sort(onlyB)
	return onlyA, onlyB
}

func sketchOf(t *testing.T, m int, keys map[uint64]struct{}) *IBLT {
	t.Helper()
	s, err := New(m)
	if err != nil {
		t.Fatal(err)
	}
	for x := range keys {
		s.Add(x)
	}
	return s
}

// adversarialPair builds an undecodable configuration at m=300 with four
// keys on one side. Their six occupied cells (two per segment) each have
// degree 2, forming a non-peelable 3-uniform hypergraph core:
//
//	k1: A B C
//	k2: A D E
//	k3: F B E
//	k4: F D C
//
// Keys are rejection-sampled against the real position function, so they
// stay pseudo-random uint64 values. The symmetric difference is 4.
func adversarialPair(t *testing.T, rng *rand.Rand) (extraA, extraB []uint64) {
	t.Helper()
	const seg = 300 / 3

	pick := func(rng *rand.Rand, want [3]int, mask [3]bool) (uint64, bool) {
		for attempt := 0; attempt < 3_000_000; attempt++ {
			x := rng.Uint64()
			p := positions(x, seg)
			ok := true
			for i := 0; i < 3; i++ {
				if mask[i] && p[i] != want[i] {
					ok = false
				}
			}
			if ok {
				return x, true
			}
		}
		return 0, false
	}

	for {
		var k [4]uint64
		k[0] = rng.Uint64()
		p0 := positions(k[0], seg)

		// k2 shares k1's segment-0 cell.
		x, ok := pick(rng, p0, [3]bool{true, false, false})
		if !ok {
			continue
		}
		k[1] = x
		p1 := positions(k[1], seg)

		// k3 shares k1's segment-1 cell and k2's segment-2 cell.
		x, ok = pick(rng, [3]int{0, p0[1], p1[2]}, [3]bool{false, true, true})
		if !ok {
			continue
		}
		k[2] = x
		p2 := positions(k[2], seg)

		// k4 closes the core: k3 seg0, k2 seg1, k1 seg2.
		x, ok = pick(rng, [3]int{p2[0], p1[1], p0[2]}, [3]bool{true, true, true})
		if !ok {
			continue
		}
		k[3] = x

		extraA = append(extraA, k[:]...)
		slices.Sort(extraA)
		return extraA, nil
	}
}

func compact(keys []uint64) string {
	if len(keys) == 0 {
		return "[]"
	}
	out := "["
	for i, x := range keys {
		if i > 0 {
			out += ","
		}
		out += fmt.Sprintf("%#x", x)
	}
	return out + "]"
}

// TestRandomDifferential drives 2000 random set pairs with symmetric
// difference at most 8 through m=300 sketches and compares every successful
// decode against the naive set difference. Decode failures are counted and
// logged but not fatal.
func TestRandomDifferential(t *testing.T) {
	const m = 300
	const cases = 2000
	const maxDiff = 8

	rng := rand.New(rand.NewPCG(0x1B17533D, 0xDEC0DE))

	successes := 0
	decodeFailures := 0

	for n := 0; n < cases; n++ {
		// Every 200-th case is a collision-seeded undecodable pair; the
		// rest are uniform random pairs. Shared keys cancel exactly under
		// Subtract, leaving only the symmetric difference in the sketch.
		baseSize := 60 + rng.IntN(60)
		adversarial := n%200 == 199
		var diffA, diffB int
		var advA, advB []uint64
		var diffSize int
		if adversarial {
			advA, advB = adversarialPair(t, rng)
			diffA, diffB = len(advA), len(advB)
		} else {
			diffSize = rng.IntN(maxDiff + 1)
			diffA = rng.IntN(diffSize + 1)
			diffB = diffSize - diffA
		}
		diffSize = diffA + diffB

		pick := func() map[uint64]struct{} { return make(map[uint64]struct{}) }
		setA := pick()
		setB := pick()

		for i := 0; i < baseSize; i++ {
			x := rng.Uint64()
			setA[x] = struct{}{}
			setB[x] = struct{}{}
		}
		extraA := make([]uint64, 0, diffA)
		for i := 0; i < diffA; i++ {
			var x uint64
			if adversarial {
				x = advA[i]
			} else {
				x = rng.Uint64()
			}
			setA[x] = struct{}{}
			extraA = append(extraA, x)
		}
		extraB := make([]uint64, 0, diffB)
		for i := 0; i < diffB; i++ {
			var x uint64
			if adversarial {
				x = advB[i]
			} else {
				x = rng.Uint64()
			}
			setB[x] = struct{}{}
			extraB = append(extraB, x)
		}
		slices.Sort(extraA)
		slices.Sort(extraB)

		wantA, wantB := naiveDifference(setA, setB)

		sa := sketchOf(t, m, setA)
		sb := sketchOf(t, m, setB)
		diff, err := Subtract(sa, sb)
		if err != nil {
			t.Fatalf("case %d: subtract: %v", n, err)
		}
		gotA, gotB, derr := diff.Decode(maxDiff)

		if adversarial {
			if derr == nil {
				t.Fatalf("case %d: adversarial configuration decoded unexpectedly: %s %s",
					n, compact(gotA), compact(gotB))
			}
			decodeFailures++
			t.Logf("case %d/%d INPUT [adversarial] |A|=%d |B|=%d diff=%d onlyA=%s onlyB=%s; "+
				"OUTPUT decode error=%v; BASIS=six occupied cells each degree 2, "+
				"no pure cell exists (naive answer onlyA=%s onlyB=%s)",
				n, cases, len(setA), len(setB), diffSize,
				compact(extraA), compact(extraB), derr,
				compact(wantA), compact(wantB))
			continue
		}

		if derr != nil {
			decodeFailures++
			t.Logf("case %d/%d INPUT [random] |A|=%d |B|=%d diff=%d onlyA=%s onlyB=%s; "+
				"OUTPUT decode error=%v; BASIS=peeling stuck with non-zero "+
				"cells (naive answer onlyA=%s onlyB=%s)",
				n, cases, len(setA), len(setB), diffSize,
				compact(extraA), compact(extraB), derr,
				compact(wantA), compact(wantB))
			continue
		}

		successes++
		if !slices.Equal(gotA, wantA) || !slices.Equal(gotB, wantB) {
			t.Fatalf("case %d mismatch:\n want onlyA=%s onlyB=%s\n got  onlyA=%s onlyB=%s",
				n, compact(wantA), compact(wantB), compact(gotA), compact(gotB))
		}
		t.Logf("case %d/%d INPUT [random] |A|=%d |B|=%d diff=%d onlyA=%s onlyB=%s; "+
			"OUTPUT onlyA=%s onlyB=%s; BASIS=all cells peeled to zero and "+
			"sorted result equals naive set difference",
			n, cases, len(setA), len(setB), diffSize,
			compact(extraA), compact(extraB), compact(gotA), compact(gotB))
	}

	t.Logf("SUMMARY cases=%d successes=%d decodeFailures=%d (failures need not "+
		"be zero; every success matched the naive difference)",
		cases, successes, decodeFailures)
}
