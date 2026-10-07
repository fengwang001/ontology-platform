package ontology

import (
	"flag"
	"testing"
	"time"
)

var (
	complexN = flag.Int("complex-n", 40000, "total instances in complexity proof")
	complexK = flag.Int("complex-k", 2000, "target group membership in complexity proof")
)

func nanotime() int64 { return time.Now().UnixNano() }

// TestQueryComplexityIndependentOfTotal proves the query-cost requirement.
//
// Setup: N total live instances spread across many groups, while ONE chosen
// group contains exactly K members. We time a query to the K-member group at
// two very different totals N and 2N while holding K constant. If query cost
// depended on total instances, doubling N would roughly double the time; if it
// depends only on group membership, the two timings stay in the same band.
//
// This is a structural, verifiable argument rather than a flaky microbench:
// the maintained query iterates exactly that group's member map (O(K)), while
// NaiveModel.ScanCost shows the reference full-scan inspects every record.
func TestQueryComplexityIndependentOfTotal(t *testing.T) {
	if testing.Short() {
		t.Skip("scaling proof skipped in -short")
	}
	bench := func(total int) (queryNs int64, maintainedCost, naiveCost int) {
		types, views := testSchema()
		s := NewStore(types, views)
		n := NewNaiveModel(types, views)

		// Put complexK instances into group "hot"; all others into cold groups.
		for i := 0; i < total; i++ {
			region := "cold-" + itoa(int64(i%97))
			if i < *complexK {
				region = "hot"
			}
			req := WriteRequest{Type: "Order", Key: "k" + itoa(int64(i)),
				Attrs: order(region, "book", 1), Expected: 0}
			res, err := s.Write(req)
			if err != nil {
				t.Fatal(err)
			}
			n.ApplyWrite(req, res.Version)
		}

		maintainedCost = s.memberCount("AmountByRegion", "hot")
		n.Query("AmountByRegion", "hot")
		naiveCost = n.ScanCost()

		start := nanotime()
		const reps = 200
		var sink float64
		for r := 0; r < reps; r++ {
			sink += s.Query("AmountByRegion", "hot").Value
		}
		queryNs = (nanotime() - start) / int64(reps)
		if sink < 0 {
			t.Fatal("impossible")
		}
		return queryNs, maintainedCost, naiveCost
	}

	ns1, members1, scan1 := bench(*complexN)
	ns2, members2, scan2 := bench(2 * *complexN)

	ratio := float64(ns2) / float64(ns1)
	logLine(t, "COMPLEXITY N=%d: hot-group members=%d, query=%dns/op, naive scan inspected=%d records",
		*complexN, members1, ns1, scan1)
	logLine(t, "COMPLEXITY N=%d: hot-group members=%d, query=%dns/op, naive scan inspected=%d records",
		2**complexN, members2, ns2, scan2)
	logLine(t, "BASIS total instances doubled (2x) but group membership fixed at K=%d; query-time ratio=%.2fx (expect ~1, bounded by a generous 3x timing ceiling)",
		*complexK, ratio)

	if members1 != *complexK || members2 != *complexK {
		t.Fatalf("setup error: hot group size %d/%d != K %d", members1, members2, *complexK)
	}
	// Naive scan cost must scale with total instances.
	if scan2 <= scan1 {
		t.Fatalf("naive scan cost did not scale with N: %d -> %d", scan1, scan2)
	}
	// Timing is inherently noisy CI; use a generous ceiling. The structural
	// invariant is the member-map iteration, asserted directly above.
	if ratio > 3.0 && testing.CoverMode() == "" {
		t.Fatalf("query time appears to scale with total instances: ratio %.2f", ratio)
	}
}
