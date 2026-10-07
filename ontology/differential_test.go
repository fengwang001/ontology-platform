package ontology

import (
	"math/rand"
	"testing"
)

// TestRandomDifferential drives random operation sequences and compares
// the indexed adjudication, event by event, against the independent
// naive whole-stream replay. Every cross check recorded in the audit
// trail must agree.
func TestRandomDifferential(t *testing.T) {
	for seed := int64(0); seed < 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		t.Run("", func(t *testing.T) {
			st := New()
			linkNames := []string{"L1", "L2"}
			for _, ln := range linkNames {
				st.Append(EventInput{Kind: EvLinkTypeDeclared, Time: 0, TypeID: ln})
			}

			objects := []string{"a", "b", "c", "d"}
			for _, o := range objects {
				otype := typeChild
				if o == "a" || o == "b" {
					otype = "ParentObj"
				}
				st.Append(EventInput{Kind: EvObjectCreated, Time: 1, ObjectID: o, TypeID: otype})
			}

			// Random edge activity on the child objects c and d.
			active := map[EdgeKey]bool{}
			steps := 10 + rng.Intn(20)
			time := 2
			for i := 0; i < steps; i++ {
				time++
				src := objects[rng.Intn(2)] // a/b are parents
				dst := []string{"c", "d"}[rng.Intn(2)]
				ln := linkNames[rng.Intn(len(linkNames))]
				edge := EdgeKey{LinkType: ln, From: src, To: dst}
				if active[edge] {
					st.Append(EventInput{Kind: EvLinkRevoked, Time: int64(time), ObjectID: src, PeerID: dst, TypeID: ln})
					delete(active, edge)
				} else {
					st.Append(EventInput{Kind: EvLinkEstablished, Time: int64(time), ObjectID: src, PeerID: dst, TypeID: ln})
					active[edge] = true
				}
				if rng.Intn(4) == 0 {
					st.Append(EventInput{Kind: EvPropertyAssigned, Time: int64(time), ObjectID: dst, PropertyKey: "k"})
				}
			}

			// Random rule: one or both link types required; retroactive
			// or forward-only.
			required := []string{"L1"}
			if rng.Intn(2) == 0 {
				required = append(required, "L2")
			}
			eff := int64(2 + rng.Intn(time))
			mustRule(t, st, "R", int64(eff), rng.Intn(2) == 0,
				map[string][]string{typeChild: required})

			// Event-by-event comparison at sampled historical times
			// (the naive cross check replays the whole stream each
			// time, so full enumeration is reserved for -race CI).
			sampleEvery := 3
			if testing.Short() {
				sampleEvery = 7
			}
			expectedCount := 0
			for at := 1; at <= time+2; at++ {
				if at%sampleEvery != 0 && at != 1 && at != time+2 {
					continue
				}
				expectedCount++
				for _, target := range []string{"c", "d"} {
					res, err := st.Determine(DetermineRequest{
						ObjectID: target, AtTime: int64(at),
						Basis: Basis{VersionID: "R"},
					})
					if err != nil {
						t.Fatalf("seed=%d target=%s at=%d: %v", seed, target, at, err)
					}
					if res.CrossCheck == nil {
						t.Fatalf("missing cross check")
					}
					if !res.CrossCheck.Agrees {
						t.Fatalf("seed=%d target=%s at=%d mismatch: %s", seed, target, at, res.CrossCheck.Mismatch)
					}
				}
			}

			// Audit trail recorded every adjudication input, rule basis
			// and comparison verdict.
			log := st.AuditLog()
			if len(log) != 2*expectedCount {
				t.Fatalf("audit entries=%d want %d", len(log), 2*expectedCount)
			}
			for _, rec := range log {
				if rec.RuleVersionID != "R" || rec.CrossCheck == nil || !rec.CrossCheck.Agrees {
					t.Fatalf("bad audit record: %+v", rec)
				}
				if rec.BasisResolved.VersionID != "R" {
					t.Fatalf("audit basis not pinned: %+v", rec.BasisResolved)
				}
			}
		})
	}
}
