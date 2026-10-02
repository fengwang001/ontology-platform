package pagecache

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// realReason explains the decision of the real implementation, mirroring the
// oracle's reason strings for the differential log.
func realReason(err error) string {
	switch {
	case err == nil:
		return "accepted"
	case errors.Is(err, ErrParam):
		return "ErrParam"
	case errors.Is(err, ErrClock):
		return "ErrClock"
	case errors.Is(err, ErrNoLimit):
		return "ErrNoLimit"
	case errors.Is(err, ErrExists):
		return "ErrExists"
	case errors.Is(err, ErrSizeMismatch):
		return "ErrSizeMismatch"
	case errors.Is(err, ErrCapacity):
		return "ErrCapacity"
	case errors.Is(err, ErrLimit):
		return "ErrLimit"
	case errors.Is(err, ErrNotFound):
		return "ErrNotFound"
	default:
		return "unknown error"
	}
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2000-sequence differential fuzz in -short mode")
	}
	const sequences = 2000
	const opsPerSequence = 120

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*1000003 + 42))
		var log strings.Builder
		fmt.Fprintf(&log, "=== differential sequence %d ===\n", seq)

		cap := int64(30 + rng.Intn(470))
		c, err := New(cap)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(cap)
		fmt.Fprintf(&log, "input New(cap=%d) -> ok\n", cap)

		const cgCount = 4
		cgs := []string{"A", "B", "C", "D"}
		type mappingKey struct {
			cg   string
			name string
		}
		var live []mappingKey
		mapCounter := map[string]int{}
		clock := int64(0)

		pageSize := func(id int) int64 {
			return int64(1 + (id*13+7)%80)
		}

		assertState := func(op string) {
			t.Helper()
			got := realSnapshot(c)
			want := modelSnapshot(m)
			if got != want {
				t.Fatalf("sequence %d diverged after %s\n--- real ---\n%s\n--- model ---\n%s\nlog:\n%s",
					seq, op, got, want, log.String())
			}
		}

		for op := 0; op < opsPerSequence; op++ {
			switch rng.Intn(10) {
			case 0: // SetLimit (possibly first registration or an update)
				cg := cgs[rng.Intn(cgCount)]
				bytes := int64(rng.Intn(200))
				e1 := c.SetLimit(cg, bytes)
				e2, why := m.setLimit(cg, bytes)
				fmt.Fprintf(&log, "input SetLimit(cg=%s bytes=%d) -> real=%v model=%v (%s)\n",
					cg, bytes, realReason(e1), realReason(e2), why)
				if (e1 == nil) != (e2 == nil) {
					t.Fatalf("SetLimit mismatch seq=%d\n%s", seq, log.String())
				}

			case 1, 2, 3, 4, 5: // Map
				cg := cgs[rng.Intn(cgCount)]
				var name string
				var pages []Page
				if rng.Intn(5) == 0 { // reuse an existing name of this cg
					for _, k := range live {
						if k.cg == cg {
							name = k.name
							break
						}
					}
				}
				if name == "" {
					mapCounter[cg]++
					name = fmt.Sprintf("m%d", mapCounter[cg])
				}
				nPages := 1 + rng.Intn(6)
				for j := 0; j < nPages; j++ {
					id := rng.Intn(10)
					size := pageSize(id)
					if rng.Intn(15) == 0 { // occasional deliberate mismatch
						size++
					}
					pages = append(pages, Page{ID: fmt.Sprintf("p%d", id), Size: size})
				}
				// Occasional stale clock; otherwise monotone-ish.
				useClock := clock
				if rng.Intn(8) == 0 {
					useClock = clock - int64(1+rng.Intn(3))
				} else {
					clock += int64(rng.Intn(3))
					useClock = clock
				}
				e1 := c.Map(useClock, cg, name, pages)
				e2, why := m.mapOp(useClock, cg, name, pages)
				fmt.Fprintf(&log, "input Map(now=%d cg=%s name=%s pages=%v) -> real=%s model=%s | %s\n",
					useClock, cg, name, pages, realReason(e1), realReason(e2), why)
				if (e1 == nil) != (e2 == nil) || realReason(e1) != realReason(e2) {
					t.Fatalf("Map mismatch seq=%d op=%d\n%s", seq, op, log.String())
				}
				if e1 == nil {
					live = append(live, mappingKey{cg, name})
					clock = useClock
				}

			default: // Unmap
				cg := cgs[rng.Intn(cgCount)]
				name := fmt.Sprintf("ghost%d", rng.Intn(5))
				chosenIdx := -1
				if len(live) > 0 && rng.Intn(5) != 0 {
					idx := rng.Intn(len(live))
					cg = live[idx].cg
					name = live[idx].name
					chosenIdx = idx
				}
				useClock := clock
				if rng.Intn(8) == 0 {
					useClock = clock - int64(1+rng.Intn(3))
				} else {
					clock += int64(rng.Intn(3))
					useClock = clock
				}
				// Ghost unmap must not consume a live slot we just removed;
				// the oracle returns ErrNotFound and state is unchanged.
				e1 := c.Unmap(useClock, cg, name)
				e2, why := m.unmapOp(useClock, cg, name)
				fmt.Fprintf(&log, "input Unmap(now=%d cg=%s name=%s) -> real=%s model=%s | %s\n",
					useClock, cg, name, realReason(e1), realReason(e2), why)
				if (e1 == nil) != (e2 == nil) || realReason(e1) != realReason(e2) {
					t.Fatalf("Unmap mismatch seq=%d op=%d\n%s", seq, op, log.String())
				}
				if e1 == nil {
					clock = useClock
					if chosenIdx >= 0 {
						live[chosenIdx] = live[len(live)-1]
						live = live[:len(live)-1]
					}
				}
			}

			if op%7 == 0 {
				// Cross-check queries against recomputed model values too.
				for _, cg := range cgs {
					if c.Total() != m.total() {
						t.Fatalf("Total mismatch seq=%d: %d vs %d\n%s", seq, c.Total(), m.total(), log.String())
					}
					sz := m.residentPages()
					u := m.usedFrom(owners(m.maps, m.since), sz)
					if c.Used(cg) != u[cg] || c.Logical(cg) != m.logical(cg) || c.Over(cg) != m.over(cg) {
						t.Fatalf("query mismatch seq=%d cg=%s\n%s", seq, cg, log.String())
					}
				}
				if c.rescans != 0 {
					t.Fatalf("rescans=%d, queries must be incremental", c.rescans)
				}
			}
			assertState(fmt.Sprintf("op %d", op))
		}

		if seq%250 == 0 {
			t.Logf("sequence %d reproduced identical ownership/usage across %d ops\n%s",
				seq, opsPerSequence, log.String())
		}
	}
}

// realSnapshot extracts the full semantic state of the real cache.
func realSnapshot(c *Cache) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var b strings.Builder
	fmt.Fprintf(&b, "clock=%d total=%d\n", c.now, c.total)

	cgNames := make([]string, 0, len(c.groups))
	for name := range c.groups {
		cgNames = append(cgNames, name)
	}
	sort.Strings(cgNames)
	for _, name := range cgNames {
		g := c.groups[name]
		var logical int64
		seen := map[string]bool{}
		for _, m := range g.maps {
			for _, p := range m.pages {
				if !seen[p.ID] {
					seen[p.ID] = true
					logical += p.Size
				}
			}
		}
		fmt.Fprintf(&b, "cg %s limit=%d used=%d logical=%d over=%v\n",
			name, g.limit, g.used, logical, g.used > g.limit)
		mnames := make([]string, 0, len(g.maps))
		for n := range g.maps {
			mnames = append(mnames, n)
		}
		sort.Strings(mnames)
		for _, n := range mnames {
			m := g.maps[n]
			ids := make([]string, len(m.pages))
			for i, p := range m.pages {
				ids[i] = fmt.Sprintf("%s:%d", p.ID, p.Size)
			}
			sort.Strings(ids)
			fmt.Fprintf(&b, "  map %s -> %v\n", n, ids)
		}
	}

	pageIDs := make([]string, 0, len(c.pages))
	for id := range c.pages {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, id := range pageIDs {
		pi := c.pages[id]
		fmt.Fprintf(&b, "page %s size=%d owner=%s holders=", id, pi.size, pi.owner)
		hs := make([]string, 0, len(pi.hold))
		for hg, h := range pi.hold {
			hs = append(hs, fmt.Sprintf("%s(ref=%d,since=%d)", hg, h.ref, h.since))
		}
		sort.Strings(hs)
		fmt.Fprintf(&b, "%v\n", hs)
	}
	return b.String()
}

// modelSnapshot renders the oracle state in the same shape as realSnapshot.
func modelSnapshot(m *naiveModel) string {
	sz := m.residentPages()
	owner := owners(m.maps, m.since)
	used := m.usedFrom(owner, sz)
	var b strings.Builder
	var total int64
	for _, s := range sz {
		total += s
	}
	fmt.Fprintf(&b, "clock=%d total=%d\n", m.now, total)

	cgNames := make([]string, 0)
	for cg := range m.limits {
		cgNames = append(cgNames, cg)
	}
	sort.Strings(cgNames)
	for _, name := range cgNames {
		log := m.logical(name)
		fmt.Fprintf(&b, "cg %s limit=%d used=%d logical=%d over=%v\n",
			name, m.limits[name], used[name], log, used[name] > m.limits[name])
		mnames := make([]string, 0)
		for n := range m.maps[name] {
			mnames = append(mnames, n)
		}
		sort.Strings(mnames)
		for _, n := range mnames {
			pages := m.maps[name][n]
			ids := make([]string, len(pages))
			for i, p := range pages {
				ids[i] = fmt.Sprintf("%s:%d", p.ID, p.Size)
			}
			sort.Strings(ids)
			fmt.Fprintf(&b, "  map %s -> %v\n", n, ids)
		}
	}

	pageIDs := make([]string, 0, len(sz))
	for id := range sz {
		pageIDs = append(pageIDs, id)
	}
	sort.Strings(pageIDs)
	for _, id := range pageIDs {
		fmt.Fprintf(&b, "page %s size=%d owner=%s holders=", id, sz[id], owner[id])
		hs := []string{}
		r := m.refs(id)
		cgs := make([]string, 0, len(r))
		for cg := range r {
			cgs = append(cgs, cg)
		}
		sort.Strings(cgs)
		for _, cg := range cgs {
			hs = append(hs, fmt.Sprintf("%s(ref=%d,since=%d)", cg, r[cg], m.since[cg+"\x00"+id]))
		}
		fmt.Fprintf(&b, "%v\n", hs)
	}
	return b.String()
}
