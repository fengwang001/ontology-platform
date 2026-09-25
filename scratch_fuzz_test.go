package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// Scratch exploration: randomly generate writes and inspect
// corrections/AsOf geometry. Deleted before finishing.
func TestScratchFuzzGeometry(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	maxVisibleMulti := 0
	dupTxFromSeen := 0
	sameValueExtraSeen := 0
	for iter := 0; iter < 20000; iter++ {
		s := NewStore()
		n := 1 + rng.Intn(6)
		for w := 0; w < n; w++ {
			vf := int64(rng.Intn(20))
			vt := vf + int64(rng.Intn(8))
			if rng.Intn(5) == 0 {
				vt = 0
			}
			if vt != 0 && vt == vf {
				vt++
			}
			tx := int64(1 + w)
			v := rng.Intn(3)
			_ = s.Write("e", "p", v, ts(vf), tsOrZero(vt), ts(tx))
		}
		for p := int64(-1); p <= 22; p++ {
			facts := s.entity("e").props["p"]
			var coverVisible []*Fact
			for _, f := range facts {
				if contains(f.ValidFrom, f.ValidTo, ts(p)) && f.visibleAt(ts(1000)) {
					coverVisible = append(coverVisible, f)
				}
			}
			if len(coverVisible) > maxVisibleMulti {
				maxVisibleMulti = len(coverVisible)
			}
			corr, err := s.Corrections("e", "p", ts(p))
			if err != nil {
				continue
			}
			for i := 1; i < len(corr); i++ {
				if corr[i].TxFrom.Equal(corr[i-1].TxFrom) {
					dupTxFromSeen++
				}
			}
			distinct := map[any]bool{}
			for _, c := range corr {
				distinct[c.Value] = true
			}
			if len(corr) > len(distinct) {
				sameValueExtraSeen++
			}
		}
	}
	fmt.Printf("FUZZ maxVisibleCovering=%d dupTxFrom=%d sameValueExtra=%d\n",
		maxVisibleMulti, dupTxFromSeen, sameValueExtraSeen)
}

func tsOrZero(sec int64) (t timeLike) { return timeLike{} }

type timeLike = struct{}
