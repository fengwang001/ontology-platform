package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestDeterminationCostIndependentOfGlobalStreamSize proves the indexed
// path's work does not grow linearly with the network's total
// create/revoke volume: unrelated objects are never scanned.
func TestDeterminationCostIndependentOfGlobalStreamSize(t *testing.T) {
	measure := func(unrelated int) int {
		st := New()
		st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
		st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "P"})
		st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
		st.Append(EventInput{Kind: EvLinkEstablished, Time: 5, ObjectID: "p", PeerID: "c", TypeID: linkParent})
		st.Append(EventInput{Kind: EvLinkRevoked, Time: 10, ObjectID: "p", PeerID: "c", TypeID: linkParent})

		// Flood the network with unrelated create/revoke traffic.
		for i := 0; i < unrelated; i++ {
			a := fmt.Sprintf("u%d", i)
			st.Append(EventInput{Kind: EvObjectCreated, Time: 2, ObjectID: a, TypeID: "P"})
			st.Append(EventInput{Kind: EvLinkEstablished, Time: 3, ObjectID: a, PeerID: "p", TypeID: linkParent})
			st.Append(EventInput{Kind: EvLinkRevoked, Time: 4, ObjectID: a, PeerID: "p", TypeID: linkParent})
		}
		mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
		res := mustDetermine(t, st, "c", 50)
		return res.EventsScanned
	}

	small := measure(100)
	large := measure(10000)
	if small != large {
		t.Fatalf("indexed scan grew with global stream: small=%d large=%d", small, large)
	}
	// The target's slice contains exactly its 5 own events plus the
	// declare event only appears in global, not per-object slices.
	if small != 3 {
		t.Fatalf("scan=%d want 3 (create + establish + revoke)", small)
	}

	// Naive model, by contrast, scans the whole stream; cross-check
	// records that cost explicitly so the independence claim is
	// independently verifiable.
	st := New()
	buildChildStream(st, 5, 10)
	for i := 0; i < 500; i++ {
		st.Append(EventInput{Kind: EvObjectCreated, Time: 2, ObjectID: fmt.Sprintf("x%d", i), TypeID: "P"})
	}
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})
	res, err := st.Determine(DetermineRequest{ObjectID: "c", AtTime: 50})
	if err != nil {
		t.Fatalf("cross-check determine: %v", err)
	}
	if res.CrossCheck == nil || !res.CrossCheck.Agrees {
		t.Fatalf("cross check=%+v", res.CrossCheck)
	}
	if res.CrossCheck.EventsScanned <= res.EventsScanned {
		t.Fatalf("naive scan %d should exceed indexed scan %d", res.CrossCheck.EventsScanned, res.EventsScanned)
	}
}

// TestConcurrentSerializability: interleave appends, rule adjustments
// and pinned determinations under -race semantics; every observed
// outcome must be consistent with some serial order.
func TestConcurrentSerializability(t *testing.T) {
	st := New()
	st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: linkParent})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "p", TypeID: "P"})
	st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: "c", TypeID: typeChild})
	mustRule(t, st, "R1", 2, true, map[string][]string{typeChild: {linkParent}})

	const writers = 8
	const rounds = 40
	var wg sync.WaitGroup

	// Appenders add property events to c (concurrent with everything).
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				st.Append(EventInput{
					Kind:        EvPropertyAssigned,
					Time:        int64(100 + w*rounds + i),
					ObjectID:    "c",
					PropertyKey: fmt.Sprintf("w%d", w),
				})
			}
		}(w)
	}

	// Readers pin immutable versions; they must never see a torn state.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				_, err := st.Determine(DetermineRequest{
					ObjectID:       "c",
					AtTime:         20000,
					Basis:          Basis{VersionID: "R1"},
					SkipCrossCheck: true,
				})
				if err != nil {
					t.Errorf("pinned concurrent determine: %v", err)
					return
				}
			}
		}()
	}

	// One rule adjuster installs forward-only versions.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5; i++ {
			h := st.HeadRuleVersion()
			_ = st.AdjustRule(RuleVersion{
				ID:               fmt.Sprintf("RX%d", i),
				EffectiveFrom:    int64(1000 + i),
				EffectiveFromSeq: h.EffectiveFromSeq + 1,
				Retroactive:      false,
				Requirement:      map[string][]string{typeChild: {linkParent}},
			})
		}
	}()

	wg.Wait()
	initial := 3 // link-type declare + two object creates
	want := initial + writers*rounds
	if got := st.StreamLength(); got != want {
		t.Fatalf("stream length=%d want %d (lost or duplicated events)", got, want)
	}
}
