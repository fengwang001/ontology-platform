package medschedtest

import (
	"fmt"
	"testing"
)

func TestFindAnyMismatch(t *testing.T) {
	var s2 int
	for s := 0; s < 1500; s++ {
		s2 = s
		seed := uint64(0x9E3779B97F4A7C15) + uint64(s)*2654435761
		path := fmt.Sprintf("/tmp/any_%d.log", s)
		sys, nv, h, r := buildRepro(t, seed, path)
		now := int64(0)
		for i := 0; i < 60; i++ {
			now = now + int64(r.intn(40))
			switch r.intn(10) {
			case 0, 1:
				h.openOrder(r, now)
			case 2, 3:
				nid, ok := h.randomNvOrder(r)
				if !ok { continue }
				rid := h.n2id[nid]
				err := sys.Administer(now, rid)
				nr := nv.Administer(now, nid)
				sc := sysCode(err)
				nc := 0
				if !nr.OK { nc = nr.Code }
				if sc != nc {
					t.Fatalf("ADM seq=%d now=%d rid=%s sc=%d nc=%d", s, now, rid, sc, nc)
				}
			case 4:
				nid, ok := h.randomNvOrder(r)
				if !ok { continue }
				rid := h.n2id[nid]
				err := sys.Refuse(now, rid)
				nr := nv.Refuse(now, nid)
				sc := sysCode(err)
				nc := 0
				if !nr.OK { nc = nr.Code }
				if sc != nc {
					o := nv.orders[nid]
					fmt.Printf("REFUSE seq=%d now=%d nid=%s kind=%d anchor=%d stop=%d\n", s2, now, nid, o.kind, o.activeAnchor, o.stoppedAt)
					for _, pp := range o.points {
						if pp.t >= now-12 && pp.t <= now+12 {
							fmt.Printf("   pt=%d st=%d\n", pp.t, pp.status)
						}
					}
					t.Fatalf("REFUSE mismatch sc=%d nc=%d", sc, nc)
				}
			case 5:
				h.makeUp(r, now)
			case 6:
				h.stop(r, now)
			case 7:
				h.revise(r, now)
			case 8:
				h.prn(r, now)
			default:
				h.query(r, now)
			}
		}
	}
}
