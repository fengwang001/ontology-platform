package ontology_test

import (
	"math"
	"math/rand"
	"os"
	"testing"

	"ontology/changelog"
	"ontology/naive"
	"ontology/ontology"
)

// Differential test: a random, interleaved stream of property writes and link
// creates/deletes against the incremental engine, cross-checked after EVERY
// operation against an independent full-recomputation model over the same raw
// store. Every change is logged with its input, touched groups and rationale.
func TestDifferentialRandomOps(t *testing.T) {
	for _, policy := range []ontology.ContributionPolicy{ontology.PolicyFullEach, ontology.PolicyDenyMulti} {
		name := "fullEach"
		if policy == ontology.PolicyDenyMulti {
			name = "denyMulti"
		}
		t.Run(name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(20261007))
			const nGroups, nMembers, steps = 4, 6, 600

			s := ontology.NewStore()
			eng := ontology.NewEngine(s)
			must(t, eng.RegisterView(ontology.ViewDef{
				Name: "v", GroupType: "g", AggType: "m",
				LinkType: "link", ValueProperty: "score", Contribution: policy,
			}))
			ref := naive.New(ontology.ViewDef{
				Name: "v", GroupType: "g", AggType: "m",
				LinkType: "link", ValueProperty: "score", Contribution: policy,
			})

			groups := make([]string, nGroups)
			for i := range groups {
				groups[i] = gid(i)
				must(t, s.CreateObject("g", groups[i]))
			}
			members := make([]string, nMembers)
			for i := range members {
				members[i] = mid(i)
				must(t, s.CreateObject("m", members[i]))
			}

			crossCheck := func(stage string) {
				t.Helper()
				liveGroups := s.ObjectIDs("g")
				for _, g := range liveGroups {
					got, err := eng.Query("v", g)
					must(t, err)
					want := ref.GroupAggregate(s, g)
					if math.Abs(got.Sum-want.Sum) > 1e-9 || got.Count != want.Count {
						t.Fatalf("[%s] %s group %s: incremental (sum=%v,count=%d) != recompute (sum=%v,count=%d)",
							stage, name, g, got.Sum, got.Count, want.Sum, want.Count)
					}
				}
			}

			for step := 0; step < steps; step++ {
				mi := rng.Intn(nMembers)
				m := members[mi]
				switch rng.Intn(7) {
				case 0, 1: // property write: concrete value, zero, or absent
					var v ontology.Optional
					switch rng.Intn(6) {
					case 0:
						v = ontology.Optional{} // clear to absent
					case 1:
						v = ontology.Optional{Present: true, Value: 0}
					default:
						v = ontology.Optional{Present: true, Value: float64(rng.Intn(21) - 10)}
					}
					if _, err := eng.SetMemberProperty("v", m, v); err != nil {
						t.Fatal(err)
					}
				case 2: // add a membership
					g := groups[rng.Intn(nGroups)]
					_, _ = eng.AddToGroup("v", m, g) // policy rejection is legal; state stays equal
				case 3: // remove a membership
					cur := s.Memberships("link", m)
					if len(cur) > 0 {
						g := cur[rng.Intn(len(cur))]
						if _, err := eng.RemoveFromGroup("v", m, g); err != nil {
							t.Fatal(err)
						}
					}
				case 4: // atomic reparent (single-membership instances only)
					if policy == ontology.PolicyDenyMulti {
						cur := s.Memberships("link", m)
						if len(cur) == 1 {
							to := groups[rng.Intn(nGroups)]
							if to != cur[0] && s.ObjectExists("g", to) {
								ver, _ := eng.MembersVersion("v", m)
								if rng.Intn(2) == 0 {
									_, _ = eng.Reparent("v", m, to, ver) // valid
								} else {
									_, _ = eng.Reparent("v", m, to, ver+1) // stale -> rejected
								}
							}
						}
					}
				case 5: // delete member, then recreate to keep the universe fixed
					if s.ObjectExists("m", m) {
						if _, err := eng.DeleteMember("v", m); err != nil {
							t.Fatal(err)
						}
						must(t, s.CreateObject("m", m))
					}
				case 6: // delete a group, then recreate it empty
					g := groups[rng.Intn(nGroups)]
					if s.ObjectExists("g", g) {
						if _, err := eng.DeleteGroup("v", g); err != nil {
							t.Fatal(err)
						}
						must(t, s.CreateObject("g", g))
					}
				}
				crossCheck("step")
			}

			// Print the change log (input, touched groups, rationale) once.
			events := eng.Events()
			if len(events) == 0 {
				t.Fatal("expected committed change events")
			}
			if testing.Verbose() {
				t.Logf("policy=%s committed events=%d; printing first 40:", name, len(events))
				changelog.PrintEvents(os.Stdout, events[:min(40, len(events))])
			} else {
				t.Logf("policy=%s committed events=%d (use -v to dump the change log)", name, len(events))
			}

			// Final full-model comparison over every group with memberships.
			full := ref.AllGroups(s)
			for _, g := range s.ObjectIDs("g") {
				got, _ := eng.Query("v", g)
				want := full[g]
				if math.Abs(got.Sum-want.Sum) > 1e-9 || got.Count != want.Count {
					t.Fatalf("final %s group %s: incremental (sum=%v,count=%d) != recompute (sum=%v,count=%d)",
						name, g, got.Sum, got.Count, want.Sum, want.Count)
				}
			}
		})
	}
}

func gid(i int) string { return "g" + itoa(i) }
func mid(i int) string { return "m" + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
