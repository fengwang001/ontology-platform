package ontology

import "testing"

func TestCountersDoNotGrowWithGraphOrGroupSize(t *testing.T) {
	// A fixed short query is executed while the total number of unrelated
	// objects, links and permission groups grows. The query-local counters
	// must remain constant.
	var baseline Counters
	for scale := 0; scale < 5; scale++ {
		g := NewGraph()
		must(t, g.AddObject("a", "A"))
		must(t, g.AddObject("b", "B"))
		must(t, g.AddLink(Link{Type: "next", From: "a", To: "b", Cost: 1}))

		// Unrelated noise that grows with scale.
		for i := 0; i < 50*(scale+1); i++ {
			x := ObjectID("x" + itoa(i))
			y := ObjectID("y" + itoa(i))
			must(t, g.AddObject(x, "X"))
			must(t, g.AddObject(y, "Y"))
			must(t, g.AddLink(Link{
				Type: LinkType("noise" + itoa(i)), From: x, To: y, Cost: 1,
			}))
		}

		ps := NewPermissionState()
		must(t, ps.UpsertGroup("g", 1))
		must(t, ps.AddMember("u", "g"))
		must(t, ps.SetLinkDecl("g", "next", Allow))
		for i := 0; i < 100*(scale+1); i++ {
			gid := GroupID("noise" + itoa(i))
			must(t, ps.UpsertGroup(gid, 100+i))
			must(t, ps.SetLinkDecl(gid, LinkType("noise"+itoa(i)), Allow))
		}

		eng := NewQueryEngine(ps, g)
		res := eng.ShortestPath(Query{Subject: "u", From: "a", To: "b"})
		if res.Status != StatusReachable {
			t.Fatalf("scale=%d status=%s", scale, res.Status)
		}
		if scale == 0 {
			baseline = res.Counters
			continue
		}
		if res.Counters != baseline {
			t.Fatalf("scale=%d counters grew: %+v vs baseline %+v",
				scale, res.Counters, baseline)
		}
	}
}

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
