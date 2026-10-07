package ontology

import (
	"flag"
	"math/rand"
	"testing"
)

var (
	dsSeeds = flag.Int("ds-seeds", 8, "number of random seeds for differential fuzz")
	dsOps   = flag.Int("ds-ops", 300, "operations per seed for differential fuzz")
)

// crossSchema uses TWO object types contributing to the SAME views, exercising
// cross-type aggregation and a shared global group-key space.
func crossSchema() ([]ObjectType, []ViewSpec) {
	types := []ObjectType{
		{Name: "Order", Attrs: map[string]AttrSpec{
			"region":  {Name: "region", Type: AttrString, Required: true},
			"channel": {Name: "channel", Type: AttrString, Required: true},
			"amount":  {Name: "amount", Type: AttrInt, Required: true},
		}},
		{Name: "Shipment", Attrs: map[string]AttrSpec{
			"region":  {Name: "region", Type: AttrString, Required: true},
			"channel": {Name: "channel", Type: AttrString, Required: true},
			"amount":  {Name: "amount", Type: AttrInt, Required: true},
		}},
	}
	views := []ViewSpec{
		{Name: "AmountByRegion", Kind: AggSum, Sources: []SourceSpec{
			{Type: "Order", GroupAttr: "region", ValueAttr: "amount", GroupValid: true},
			{Type: "Shipment", GroupAttr: "region", ValueAttr: "amount", GroupValid: true},
		}},
		{Name: "CountByChannel", Kind: AggCount, Sources: []SourceSpec{
			{Type: "Order", GroupAttr: "channel", GroupValid: true},
			{Type: "Shipment", GroupAttr: "channel", GroupValid: true},
		}},
	}
	return types, views
}

func opErrCode(err error) string {
	if err == nil {
		return "OK"
	}
	if oe, ok := err.(*OpError); ok {
		if oe.Reason != "" {
			return string(oe.Code) + "/" + string(oe.Reason)
		}
		return string(oe.Code)
	}
	return err.Error()
}

// compareEveryGroup scans the union of maintained and naive groups and asserts
// identical (exists, value, count) for each view.
func compareEveryGroup(t *testing.T, s *Store, n *NaiveModel, views []ViewSpec) {
	t.Helper()
	for _, v := range views {
		maintained := make(map[string]GroupResult)
		groups, err := s.maintainer.allGroups(v.Name)
		if err != nil {
			t.Fatal(err)
		}
		for g, r := range groups {
			maintained[g] = r
		}
		union := map[string]bool{"__probe__": true}
		for g := range maintained {
			union[g] = true
		}
		// probe the naive model for every maintained group plus a missing probe
		for g := range union {
			a := s.Query(v.Name, g)
			b := n.Query(v.Name, g)
			if a.Exists != b.Exists || a.Value != b.Value || a.Count != b.Count {
				t.Fatalf("DIFF view=%s group=%q maintained=(%v,%v,%d) naive=(%v,%v,%d)",
					v.Name, g, a.Exists, a.Value, a.Count, b.Exists, b.Value, b.Count)
			}
		}
	}
}

func TestRandomDifferential(t *testing.T) {
	types, views := crossSchema()
	typeNames := []string{"Order", "Shipment"}
	regions := []string{"east", "west", "north"}
	channels := []string{"web", "store"}
	const keyPool = 7

	for seed := 1; seed <= *dsSeeds; seed++ {
		rng := rand.New(rand.NewSource(int64(seed)))
		s := NewStore(types, views)
		n := NewNaiveModel(types, views)

		// client view of live version per type/key (0 = no live instance)
		liveVer := map[string]int64{}

		for step := 1; step <= *dsOps; step++ {
			typeName := typeNames[rng.Intn(len(typeNames))]
			key := keyName(typeName, rng.Intn(keyPool))

			if rng.Intn(10) < 7 { // WRITE (incl. migration), with possible bad inputs
				req := WriteRequest{Type: typeName, Key: key,
					Attrs: map[string]any{
						"region":  regions[rng.Intn(len(regions))],
						"channel": channels[rng.Intn(len(channels))],
						"amount":  rng.Intn(200) - 50,
					}}
				// deliberately corrupt parameters sometimes (priority class 1)
				corrupt := rng.Intn(12)
				if corrupt == 0 {
					req.Key = ""
				} else if corrupt == 1 {
					req.Attrs["amount"] = "NaN-string"
				} else if corrupt == 2 {
					req.Attrs["region"] = ""
				}
				// choose an optimistic credential
				switch rng.Intn(4) {
				case 0:
					req.Expected = 0 // create
				case 1:
					req.Expected = liveVer[key] // fresh token (0 if absent)
				default:
					req.Expected = rng.Int63n(6) // possibly stale/forbidden
				}

				res, err := s.Write(req)
				code := opErrCode(err)
				logLine(t, "[seed=%d step=%d] WRITE type=%s key=%q region=%v channel=%v amount=%v expected=%d -> %s",
					seed, step, req.Type, req.Key, req.Attrs["region"], req.Attrs["channel"],
					req.Attrs["amount"], req.Expected, code)
				if err == nil {
					n.ApplyWrite(req, res.Version)
					liveVer[key] = res.Version
					logLine(t, "    BASIS accepted: naive mirrored version=%d; maintained must equal recompute", res.Version)
				} else {
					logLine(t, "    BASIS rejected (%s): no version consumed, no aggregate touched", code)
				}
			} else { // DELETE, including never-committed and double-delete
				req := DeleteRequest{Type: typeName, Key: key}
				if rng.Intn(2) == 0 {
					req.Expected = 0
				} else {
					req.Expected = rng.Int63n(5)
				}
				if rng.Intn(15) == 0 {
					req.Key = "" // invalid argument must win
				}
				_, err := s.Delete(req)
				code := opErrCode(err)
				logLine(t, "[seed=%d step=%d] DELETE type=%s key=%q expected=%d -> %s",
					seed, step, req.Type, req.Key, req.Expected, code)
				if err == nil {
					n.ApplyDelete(req)
					liveVer[key] = 0
					logLine(t, "    BASIS accepted delete: contributions withdrawn in the same commit")
				} else {
					logLine(t, "    BASIS rejected (%s): aggregates unchanged", code)
				}
			}

			if step%25 == 0 || step == *dsOps {
				compareEveryGroup(t, s, n, views)
				if err := s.Verify(); err != nil {
					t.Fatalf("maintained vs full-recompute diverged seed=%d step=%d: %v", seed, step, err)
				}
				logLine(t, "    CHECK step=%d: every maintained group == naive full-scan recompute; Verify clean; naive scanned %d records",
					step, n.liveCount())
			}
		}
	}
}

func keyName(typeName string, i int) string {
	return typeName + "-k" + itoa(int64(i))
}
