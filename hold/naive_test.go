package hold_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/hold"
	"ontology/routing"
	"ontology/wip"
)

// piece tracks one physical unit through the line. i==0 means terminal
// (finished or scrapped); k is its accumulated rework count.
type piece struct {
	i, k  int
	done  bool
	scrap bool
}

type naiveWO struct {
	route  *routing.Route
	q      int64
	pieces []*piece
	held   bool
	fp     map[int][2]int64 // inspection op -> good, total
}

type naiveModel struct {
	routes map[string]*routing.Route
	wos    map[string]*naiveWO
	y      int
	nmin   int64
}

func (nm *naiveModel) cell(wo string, i, k int) int64 {
	var n int64
	for _, p := range nm.wos[wo].pieces {
		if !p.done && !p.scrap && p.i == i && p.k == k {
			n++
		}
	}
	return n
}

func (nm *naiveModel) totals(wo string) (done, scrapped, wipN int64) {
	for _, p := range nm.wos[wo].pieces {
		switch {
		case p.done:
			done++
		case p.scrap:
			scrapped++
		default:
			wipN++
		}
	}
	return
}

func classify(err error) string {
	switch {
	case errors.Is(err, wip.ErrReworkCap):
		return "rework-cap"
	case errors.Is(err, wip.ErrOverQty):
		return "over-qty"
	case errors.Is(err, wip.ErrState):
		return "state"
	case errors.Is(err, wip.ErrNotFound):
		return "not-found"
	case errors.Is(err, hold.ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, wip.ErrInvalid):
		return "invalid"
	case err == nil:
		return "ok"
	default:
		return "other:" + err.Error()
	}
}

func (nm *naiveModel) reportExpect(wo string, i, k int, good, scrap, rework int64) error {
	o, ok := nm.wos[wo]
	if !ok {
		return wip.ErrNotFound
	}
	if i < 1 || k < 0 || good < 0 || scrap < 0 || rework < 0 || good+scrap+rework < 1 {
		return wip.ErrInvalid
	}
	if o.held {
		return wip.ErrHeld
	}
	if i > o.route.N || k > o.route.R {
		return wip.ErrInvalid
	}
	total := good + scrap + rework
	if nm.cell(wo, i, k) < total {
		return wip.ErrOverQty
	}
	if rework > 0 && k == o.route.R {
		return wip.ErrReworkCap
	}
	return nil
}

func (nm *naiveModel) reportApply(wo string, i, k int, good, scrap, rework int64) {
	o := nm.wos[wo]
	rt := o.route
	g, s, rw := good, scrap, rework
	for _, p := range o.pieces {
		if p.done || p.scrap || p.i != i || p.k != k {
			continue
		}
		switch {
		case g > 0:
			g--
			if i == rt.N {
				p.done = true
				p.i = 0
			} else {
				p.i = i + 1
			}
		case s > 0:
			s--
			p.scrap = true
			p.i = 0
		case rw > 0:
			rw--
			p.i = rt.Back[i-1]
			p.k++
		}
	}
	if rt.Insp[i-1] && k == 0 {
		fp := o.fp[i]
		fp[0] += good
		fp[1] += good + scrap + rework
		o.fp[i] = fp
		if fp[1] >= nm.nmin && fp[0]*100 < int64(nm.y)*fp[1] {
			o.held = true
		}
	}
}

func (nm *naiveModel) splitExpect(wo, newWO string, i, k int, qty int64) error {
	o, ok := nm.wos[wo]
	if !ok {
		return wip.ErrNotFound
	}
	if newWO == "" || i < 1 || k < 0 || qty < 1 {
		return wip.ErrInvalid
	}
	if o.held {
		return wip.ErrHeld
	}
	if i > o.route.N || k > o.route.R {
		return wip.ErrInvalid
	}
	if nm.cell(wo, i, k) < qty {
		return wip.ErrOverQty
	}
	if _, exists := nm.wos[newWO]; exists {
		return wip.ErrState
	}
	return nil
}

func (nm *naiveModel) splitApply(wo, newWO string, i, k int, qty int64) {
	parent := nm.wos[wo]
	moved := make([]*piece, 0, qty)
	remaining := make([]*piece, 0, len(parent.pieces))
	var n int64
	for _, p := range parent.pieces {
		if n < qty && !p.done && !p.scrap && p.i == i && p.k == k {
			n++
			moved = append(moved, p)
		} else {
			remaining = append(remaining, p)
		}
	}
	parent.pieces = remaining
	parent.q -= qty
	nm.wos[newWO] = &naiveWO{route: parent.route, q: qty, pieces: moved, fp: map[int][2]int64{}}
}

func (nm *naiveModel) verify(t *testing.T, m *wip.Manager) {
	t.Helper()
	for id, o := range nm.wos {
		st, err := m.Snapshot(id)
		if err != nil {
			t.Fatalf("snapshot %s: %v", id, err)
		}
		done, scrapped, wipN := nm.totals(id)
		if st.Done != done || st.Scrapped != scrapped || st.WIP != wipN {
			t.Fatalf("%s totals model(done=%d scrap=%d wip=%d) impl(%d,%d,%d)",
				id, done, scrapped, wipN, st.Done, st.Scrapped, st.WIP)
		}
		if st.Q != o.q {
			t.Fatalf("%s Q model=%d impl=%d", id, o.q, st.Q)
		}
		if st.Held != o.held {
			t.Fatalf("%s held model=%v impl=%v", id, o.held, st.Held)
		}
		for x := 1; x <= o.route.N; x++ {
			for level := 0; level <= o.route.R; level++ {
				if got, want := st.At(x, level), nm.cell(id, x, level); got != want {
					t.Fatalf("%s cell(%d,%d) model=%d impl=%d", id, x, level, want, got)
				}
			}
		}
		if st.Q != done+scrapped+wipN {
			t.Fatalf("%s conservation broken", id)
		}
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g*7919 + 42)))
		n := 1 + rng.Intn(5)
		back := make([]int, n)
		insp := make([]bool, n)
		for i := range back {
			back[i] = 1 + rng.Intn(i+1)
		}
		for j := 0; j < 1+rng.Intn(n); j++ {
			insp[rng.Intn(n)] = true
		}
		r := rng.Intn(3)
		y := 30 + rng.Intn(71)
		nmin := int64(1 + rng.Intn(12))

		reg := routing.NewRegistry()
		rid := fmt.Sprintf("rt%d", g)
		if _, err := reg.Define(rid, n, back, insp, r); err != nil {
			t.Fatal(err)
		}
		m := wip.NewManager(reg)
		sys, err := hold.New(m, y, nmin)
		if err != nil {
			t.Fatal(err)
		}
		rt, _ := reg.Get(rid)
		nm := &naiveModel{
			routes: map[string]*routing.Route{},
			wos:    map[string]*naiveWO{},
			y:      y,
			nmin:   nmin,
		}

		q := int64(5 + rng.Intn(46))
		root := "wo"
		if err := m.Open(root, rid, q); err != nil {
			t.Fatal(err)
		}
		pieces := make([]*piece, q)
		for idx := range pieces {
			pieces[idx] = &piece{i: 1}
		}
		nm.wos[root] = &naiveWO{route: rt, q: q, pieces: pieces, fp: map[int][2]int64{}}

		ops := 6 + rng.Intn(20)
		for op := 0; op < ops; op++ {
			var live []string
			for id, o := range nm.wos {
				_, _, w := nm.totals(id)
				if w > 0 && !o.held {
					live = append(live, id)
				}
			}
			kind := rng.Intn(100)

			if kind >= 92 {
				// resume: mix QE/non-QE, held/non-held/ghost to exercise order.
				target := fmt.Sprintf("ghost%d", rng.Intn(3))
				for id, o := range nm.wos {
					if o.held {
						target = id
					}
				}
				isQE := rng.Intn(2) == 0
				idn := hold.Identity{Name: "op", Role: "line"}
				if isQE {
					idn.Role = hold.QE
				}
				gotErr := sys.Resume(target, idn)
				want := "state"
				if o, ok := nm.wos[target]; !ok {
					if isQE {
						want = "not-found"
					} else {
						want = "unauthorized"
					}
				} else if !isQE {
					want = "unauthorized"
				} else if !o.held {
					want = "state"
				} else {
					want = "ok"
					o.held = false
					o.fp = map[int][2]int64{}
				}
				t.Logf("g=%d op=%d Resume(%q,qe=%v) => %s (want %s); basis: y=%d nmin=%d",
					g, op, target, isQE, classify(gotErr), want, y, nmin)
				if classify(gotErr) != want {
					t.Fatalf("group %d resume: got %s want %s", g, classify(gotErr), want)
				}
				continue
			}

			if len(live) == 0 {
				break
			}
			wo := live[rng.Intn(len(live))]

			if kind >= 78 {
				type ck struct{ i, k int }
				var cells []ck
				for x := 1; x <= n; x++ {
					for level := 0; level <= r; level++ {
						if nm.cell(wo, x, level) > 0 {
							cells = append(cells, ck{x, level})
						}
					}
				}
				c := cells[rng.Intn(len(cells))]
				avail := nm.cell(wo, c.i, c.k)
				qty := int64(1 + rng.Intn(int(avail)+2))
				newWO := fmt.Sprintf("%s-c%d", wo, op)
				gotErr := sys.Split(wo, newWO, c.i, c.k, qty)
				wantErr := nm.splitExpect(wo, newWO, c.i, c.k, qty)
				t.Logf("g=%d op=%d Split(%q -> %q i=%d k=%d qty=%d avail=%d) => %s (want %s)",
					g, op, wo, newWO, c.i, c.k, qty, avail, classify(gotErr), classify(wantErr))
				if classify(gotErr) != classify(wantErr) {
					t.Fatalf("group %d split mismatch", g)
				}
				if wantErr == nil {
					nm.splitApply(wo, newWO, c.i, c.k, qty)
				}
				nm.verify(t, m)
				continue
			}

			i := 1 + rng.Intn(n)
			k := rng.Intn(r + 1)
			avail := nm.cell(wo, i, k)
			if avail == 0 {
				// still attempt an invalid report half the time
				if rng.Intn(2) == 0 {
					err := sys.Report(wo, i, k, 1, 0, 0)
					if !errors.Is(err, wip.ErrOverQty) {
						t.Fatalf("group %d empty-cell report: %v", g, err)
					}
				}
				continue
			}
			total := int64(1 + rng.Intn(int(avail)+2))
			var good, scrap, rework int64
			rest := total
			for rest > 0 {
				d := int64(1 + rng.Intn(int(rest)))
				switch rng.Intn(3) {
				case 0:
					good += d
				case 1:
					scrap += d
				default:
					rework += d
				}
				rest -= d
			}
			gotErr := sys.Report(wo, i, k, good, scrap, rework)
			wantErr := nm.reportExpect(wo, i, k, good, scrap, rework)
			basis := fmt.Sprintf("i=%d k=%d/%d avail=%d insp=%v", i, k, r, avail, rt.Insp[i-1])
			t.Logf("g=%d op=%d Report(%q g=%d s=%d rw=%d) => %s (want %s); %s",
				g, op, wo, good, scrap, rework, classify(gotErr), classify(wantErr), basis)
			if classify(gotErr) != classify(wantErr) {
				t.Fatalf("group %d report mismatch: %s vs %s (%s)", g,
					classify(gotErr), classify(wantErr), basis)
			}
			if wantErr == nil {
				nm.reportApply(wo, i, k, good, scrap, rework)
			}
			nm.verify(t, m)
		}

		// attempt closes for every resolved (wip==0) order
		for id, o := range nm.wos {
			_, _, w := nm.totals(id)
			if w != 0 || o.held {
				continue
			}
			res, err := sys.Close(id)
			if err != nil {
				t.Fatalf("group %d close %s: %v", g, id, err)
			}
			done, scrapped, _ := nm.totals(id)
			if res.Done != done || res.Scrapped != scrapped || res.Shortage != o.q-done {
				t.Fatalf("group %d close mismatch %+v model d=%d s=%d", g, res, done, scrapped)
			}
			t.Logf("g=%d Close(%q) => done=%d scrapped=%d shortage=%d",
				g, id, res.Done, res.Scrapped, res.Shortage)
		}
		nm.verify(t, m)
	}
}
