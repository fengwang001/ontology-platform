package medschedtest

import (
	"os"
	"testing"

	"ontology/medsched"
)

func buildRepro(t *testing.T, seed uint64, logPath string) (*medsched.System, *Naive, *harness, *rng) {
	t.Helper()
	const w = int64(10)
	sys, _ := medsched.New(w)
	nv := NewNaive(w)
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, sys: sys, nv: nv, logf: lf, w: w,
		drugs: []string{"D1", "D2", "D3", "D4"}, pats: []string{"P1", "P2", "P3"},
		id2n: map[string]string{}, n2id: map[string]string{},
		lastPlanned: map[string]int64{}, drugMins: map[string]int64{}}
	r := &rng{state: seed | 1}
	cats := []string{"C1", "C2"}
	for _, d := range h.drugs {
		mn := int64(5 + r.intn(30))
		cat := cats[r.intn(len(cats))]
		if err := sys.RegisterDrug(0, d, cat, mn); err != nil {
			t.Fatal(err)
		}
		if nr := nv.RegisterDrug(0, d, cat, mn); !nr.OK {
			t.Fatalf("nv reg %d", nr.Code)
		}
		h.drugMins[d] = mn
	}
	if r.intn(2) == 0 {
		_ = sys.AddAllergyDrug(0, "P2", "D1")
		_ = nv.AddAllergyDrug(0, "P2", "D1")
	}
	if r.intn(2) == 0 {
		_ = sys.AddAllergyCategory(0, "P3", "C2")
		_ = nv.AddAllergyCat(0, "P3", "C2")
	}
	return sys, nv, h, r
}

func runRepro(h *harness, r *rng, sys *medsched.System, nv *Naive) int64 {
	now := int64(0)
	for i := 0; i < 60; i++ {
		now = now + int64(r.intn(40))
		switch r.intn(10) {
		case 0, 1:
			h.openOrder(r, now)
		case 2, 3:
			h.administer(r, now)
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
	}
	return sys.Now()
}
