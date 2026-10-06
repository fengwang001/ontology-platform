package traffic

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// TestRandomDifferentialVsNaive replays the same random operation
// sequence against the event-driven Service and the independently
// written micro-stepped naiveModel and asserts field-for-field
// equality of every link at integer probe times. All event times are
// integers and the naive step is 1, so the comparison is exact.
//
// The log prints every operation with its inputs, outputs/errors and
// the decision basis; on mismatch the whole log is dumped.
func TestRandomDifferentialVsNaive(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			var log strings.Builder
			emit := func(format string, a ...any) {
				fmt.Fprintf(&log, format+"\n", a...)
			}

			n := NewNetwork()
			nodes := []string{"a", "b", "c", "d", "e"}
			idFor := make([][]string, len(nodes))
			id := 0
			// random sparse directed edges
			for u := 0; u < len(nodes); u++ {
				for v := 0; v < len(nodes); v++ {
					if u == v || rng.Intn(3) != 0 {
						continue
					}
					cap := int64(rng.Intn(9) + 2)
					arr := cap - int64(rng.Intn(int(cap)+1)) // 0..cap
					length := int64(rng.Intn(30) + 5)
					lid := fmt.Sprintf("L%d", id)
					id++
					mustAdd(t, n, lid, nodes[u], nodes[v], ri(length), ri(cap), ri(arr))
					idFor[u] = append(idFor[u], lid)
				}
			}
			if len(n.links) == 0 {
				mustAdd(t, n, "L0", "a", "b", ri(10), ri(5), ri(4))
			}
			var linkIDs []string
			for lid := range n.links {
				linkIDs = append(linkIDs, lid)
			}
			sort.Strings(linkIDs)

			svc := NewService(n, ri(1))
			var logBuf strings.Builder
			svc.SetLogger(func(line string) { fmt.Fprintln(&logBuf, line) })
			nm := newNaiveModel(n, ri(1), ri(1))

			// Shared active incident set guarantees both models receive
			// the same valid operations; rejected ops are skipped too.
			var active []string
			counter := 0
			doRegister := func(at int64) {
				iid := fmt.Sprintf("I%d", counter)
				counter++
				lid := linkIDs[rng.Intn(len(linkIDs))]
				ratio := int64(rng.Intn(4)) // 0..3 / 4
				inc := Incident{ID: iid, LinkID: lid, Start: ri(at), Ratio: r(ratio, 4)}
				if _, err := svc.Register(inc); err != nil {
					t.Fatalf("register: %v", err)
				}
				if err := nm.register(inc); err != nil {
					t.Fatalf("naive register: %v", err)
				}
				active = append(active, iid)
				emit("OP register id=%s link=%s at=%d ratio=%d/4 -> accepted | basis: start at exact tick; strongest ratio wins",
					iid, lid, at, ratio)
			}

			for at := int64(0); at <= 60; at++ {
				nops := rng.Intn(3)
				for k := 0; k < nops; k++ {
					if len(active) > 0 && rng.Intn(2) == 0 {
						idx := rng.Intn(len(active))
						iid := active[idx]
						if rng.Intn(2) == 0 {
							ratio := int64(rng.Intn(4))
							if err := svc.Update(iid, ri(at), r(ratio, 4)); err != nil {
								t.Fatalf("update: %v", err)
							}
							if err := nm.update(iid, ri(at), r(ratio, 4)); err != nil {
								t.Fatalf("naive update: %v", err)
							}
							emit("OP update   id=%s at=%d ratio=%d/4 -> accepted | basis: prior queue kept, new drift from now",
								iid, at, ratio)
						} else {
							if err := svc.Clear(iid, ri(at)); err != nil {
								t.Fatalf("clear: %v", err)
							}
							if err := nm.clear(iid, ri(at)); err != nil {
								t.Fatalf("naive clear: %v", err)
							}
							active = append(active[:idx], active[idx+1:]...)
							emit("OP clear    id=%s at=%d -> accepted | basis: restore capacity; drains by per-link difference", iid, at)
						}
					} else {
						doRegister(at)
					}
				}
				if err := svc.Advance(ri(at)); err != nil {
					t.Fatalf("advance: %v", err)
				}
				nm.runTo(ri(at))
				for _, lid := range linkIDs {
					ss, qerr := svc.Query(lid)
					if qerr != nil {
						t.Fatalf("seed %d query: %v\n%s", seed, qerr, log.String())
					}
					ns := nm.query(lid)
					if ss.Queue.Cmp(ns.Queue) != 0 || ss.Level != ns.Level {
						t.Fatalf("seed %d MISMATCH at t=%d link=%s engine(q=%s lv=%d) naive(q=%s lv=%d)\n%s\n--- engine trace ---\n%s",
							seed, at, lid, ss.Queue.RatString(), ss.Level,
							ns.Queue.RatString(), ns.Level, log.String(), logBuf.String())
					}
				}
			}
			emit("FINAL seed=%d: every link queue+level matched the naive model at all 61 ticks", seed)
		})
	}
}
