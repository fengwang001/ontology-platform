package traffic

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// TestRandomInvariants stresses a larger network and asserts the two
// global safety invariants at every tick:
//
//  1. every queue is within [0, link length/vehicle length];
//  2. every direct upstream of a spilling link has effCap no larger
//     than the spilling source's effCap.
func TestRandomInvariants(t *testing.T) {
	for seed := int64(100); seed < 130; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := NewNetwork()
		nodes := []string{"n0", "n1", "n2", "n3", "n4", "n5"}
		id := 0
		for u := range nodes {
			for v := range nodes {
				if u == v || rng.Intn(3) != 0 {
					continue
				}
				cap := int64(rng.Intn(6) + 1)
				arr := cap - int64(rng.Intn(int(cap)+1))
				length := int64(rng.Intn(20) + 2)
				mustAdd(t, n, fmt.Sprintf("L%d", id), nodes[u], nodes[v], ri(length), ri(cap), ri(arr))
				id++
			}
		}
		var linkIDs []string
		for lid := range n.links {
			linkIDs = append(linkIDs, lid)
		}
		sort.Strings(linkIDs)
		s := NewService(n, ri(1))

		var active []string
		counter := 0
		check := func(at int64) {
			t.Helper()
			for _, lid := range linkIDs {
				l := s.eng.net.links[lid]
				cv := new(Rat).Quo(l.Length, s.eng.vehLen)
				q := s.eng.queue[lid]
				if q.Sign() < 0 || q.Cmp(cv) > 0 {
					t.Fatalf("seed %d t=%d %s queue %s out of [0,%s]",
						seed, at, lid, q.RatString(), cv.RatString())
				}
				if s.eng.spill[lid] {
					for _, up := range s.eng.net.upstream[lid] {
						if s.eng.effCap[up].Cmp(s.eng.effCap[lid]) > 0 {
							t.Fatalf("seed %d t=%d effCap(%s)=%s > spilling %s effCap=%s",
								seed, at, up, s.eng.effCap[up].RatString(),
								lid, s.eng.effCap[lid].RatString())
						}
					}
				}
			}
		}
		for at := int64(0); at <= 80; at++ {
			for k := rng.Intn(4); k >= 0; k-- {
				if len(active) > 0 && rng.Intn(2) == 0 {
					idx := rng.Intn(len(active))
					iid := active[idx]
					if rng.Intn(2) == 0 {
						_ = s.Update(iid, ri(at), r(int64(rng.Intn(4)), 4))
					} else {
						if err := s.Clear(iid, ri(at)); err == nil {
							active = append(active[:idx], active[idx+1:]...)
						}
					}
				} else {
					iid := fmt.Sprintf("I%d", counter)
					counter++
					lid := linkIDs[rng.Intn(len(linkIDs))]
					if _, err := s.Register(Incident{
						ID: iid, LinkID: lid, Start: ri(at),
						Ratio: r(int64(rng.Intn(4)), 4),
					}); err == nil {
						active = append(active, iid)
					}
				}
			}
			if err := s.Advance(ri(at)); err != nil {
				t.Fatal(err)
			}
			check(at)
		}
	}
}
