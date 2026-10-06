package medschedtest

import (
	"fmt"
	"testing"
)

func TestFindAdminMismatch(t *testing.T) {
	for s := 0; s < 1500; s++ {
		seed := uint64(0x9E3779B97F4A7C15) + uint64(s)*2654435761
		sys, nv, h, r := buildRepro(t, seed, fmt.Sprintf("/tmp/adm_%d.log", s))
		now := int64(0)
		failed := false
		for i := 0; i < 60 && !failed; i++ {
			now = now + int64(r.intn(40))
			c := r.intn(10)
			var err error
			var nr NResult
			var tag string
			switch c {
			case 0, 1:
				h.openOrder(r, now)
			case 2, 3:
				nid, ok := h.randomNvOrder(r)
				if !ok { continue }
				rid := h.n2id[nid]
				err = sys.Administer(now, rid)
				nr = nv.Administer(now, nid)
				tag = "A"
			case 4:
				h.refuse(r, now)
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
			if tag == "A" {
				sc := 0
				if err != nil {
					sc = sysCode(err)
				}
				nc := 0
				if !nr.OK { nc = nr.Code }
				if sc != nc {
					fmt.Printf("MISMATCH seq=%d now=%d sysCode=%d nvCode=%d\n", s, now, sc, nc)
					failed = true
				}
			}
		}
		if failed {
			return
		}
		_ = nv
	}
}
