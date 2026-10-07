package tzperm

import (
	"fmt"
	"testing"
)

// TestLookupCostNotLinear is the independently executable proof that a
// single normalization does not pay cost linear in the accumulated region
// timezone-definition count. It compares the engine's counted comparisons
// with the naive linear scan at several scales and asserts sublinear
// growth (comparisons ~ log2 n+1).
func TestLookupCostNotLinear(t *testing.T) {
	for _, n := range []int{16, 256, 4096, 65536} {
		versions := make([]ZoneVersion, n)
		for i := range versions {
			versions[i] = ZoneVersion{
				EffectiveFrom: int64(i * 100),
				Zone:          &ZoneRules{Name: fmt.Sprintf("Z%d", i), Transitions: []Transition{{At: -1 << 62, Offset: 0}}},
			}
		}
		target := int64((n - 1) * 100)
		got := lookupEffective(versions, func(v ZoneVersion) int64 { return v.EffectiveFrom }, target)

		linear := n
		bound := 0
		for size := n; size > 1; size >>= 1 {
			bound++
		}
		bound++ // floor(log2 n)+1 comparisons max

		if got.comparisons > bound {
			t.Fatalf("n=%d comparisons=%d > log2 bound %d", n, got.comparisons, bound)
		}
		if n >= 4096 && got.comparisons >= linear/4 {
			t.Fatalf("n=%d comparisons=%d suspiciously close to linear %d", n, got.comparisons, linear)
		}
		if !got.found || got.index != n-1 {
			t.Fatalf("lookup result wrong: %+v", got)
		}
	}
}
