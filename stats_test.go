package ontology

import (
	"math"
	"testing"
)

// Weight semantics must be statistically checkable: with k=1, the
// selection probability of an element with weight w must be
// proportional to w.
//
// Setup: three elements with weights 1, 2, 3, so the expected
// inclusion probabilities are 1/6, 2/6, 3/6. We run 2000 independent
// rounds, each with its own deterministic seed derived from a fixed
// base seed, so the whole test is reproducible: the same code always
// draws the same numbers and reaches the same verdict.
//
// Tolerance: the observed frequency is a binomial proportion with
// standard error se = sqrt(p(1-p)/n). The worst case p=0.5 at n=2000
// gives se of about 0.0112. We allow 5 standard errors (about 0.056,
// rounded to 0.06), which makes a false failure essentially impossible
// while still catching any real deviation from proportionality.
func TestSelectionFrequencyProportionalToWeight(t *testing.T) {
	const rounds = 2000
	const baseSeed = 0xC0FFEE
	weights := []float64{1, 2, 3}
	counts := make([]int, len(weights))

	for round := 0; round < rounds; round++ {
		r, err := New(1, baseSeed+uint64(round))
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range weights {
			if err := r.Add(string(rune('A'+i)), w); err != nil {
				t.Fatal(err)
			}
		}
		sample := r.Sample()
		if len(sample) != 1 {
			t.Fatalf("round %d: sample size %d, want 1", round, len(sample))
		}
		counts[sample[0][0]-'A']++
	}

	totalWeight := 0.0
	for _, w := range weights {
		totalWeight += w
	}
	const tol = 0.06 // 5 standard errors, see comment above
	for i, w := range weights {
		want := w / totalWeight
		got := float64(counts[i]) / rounds
		if math.Abs(got-want) > tol {
			t.Fatalf("element %d (weight %v): frequency %.4f, want %.4f +/- %.2f",
				i, w, got, want, tol)
		}
		t.Logf("weight %v: got %.4f, want %.4f", w, got, want)
	}
}

// Sanity check on the tolerance derivation: 5*se must stay below tol
// for every p in [0,1] at the round count used above.
func TestToleranceCoversFiveSigma(t *testing.T) {
	const rounds = 2000
	se := math.Sqrt(0.25 / rounds) // worst-case binomial SE
	if 5*se >= 0.06 {
		t.Fatalf("tolerance 0.06 does not cover 5 sigma (5*se=%.4f)", 5*se)
	}
}
