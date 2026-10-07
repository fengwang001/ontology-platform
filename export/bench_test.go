package export

import (
	"fmt"
	"testing"
)

// BenchmarkAccept demonstrates that membership work does not grow with the
// link's total exported history: a fresh Deduper per cycle (the production
// shape) accepts writes at constant average cost regardless of how many
// cycles already completed on the link. Run with -benchmem.
func BenchmarkAccept(b *testing.B) {
	for _, priorHistory := range []int{0, 1_000, 100_000} {
		b.Run(fmt.Sprintf("priorHistory=%d", priorHistory), func(b *testing.B) {
			cp, h := NewMemCheckpoint(), NewMemHistory()
			m := NewManager(cp, h)
			sink := newRecSink()
			var cur Position
			// Burn through priorHistory confirmed writes, 1000 per cycle.
			for done := 0; done < priorHistory; {
				end := cur + 1000
				if Position(done)+1000 > Position(priorHistory) {
					end = Position(priorHistory)
				}
				runConfirmed(b, m, sink, "L", cur, end)
				done += int(end - cur)
				cur = end
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				d := NewDeduper()
				d.Accept(Write{Position(i + 1), fmt.Sprintf("id-%d", i)})
			}
		})
	}
}
