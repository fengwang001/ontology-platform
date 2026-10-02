package pagecache

import (
	"sort"
)

// naiveModel is an independent, deliberately simple reimplementation of the
// rules in the specification. It recomputes ownership from raw references on
// every decision and is only used as an oracle in tests.
type naiveModel struct {
	cap int64

	limits map[string]int64
	// maps[cg][name] is the stored page occurrence list of one mapping.
	maps map[string]map[string][]Page
	// now is the largest accepted Map/Unmap clock.
	now int64
	// since["cg\x00id"] is the since of cg's current holder streak of id.
	since map[string]int64
}

func newNaiveModel(cap int64) *naiveModel {
	return &naiveModel{
		cap:    cap,
		limits: map[string]int64{},
		maps:   map[string]map[string][]Page{},
		since:  map[string]int64{},
	}
}

// refs returns, for every cgroup, the summed occurrence count of id and the
// since of its current holder streak. since values are tracked explicitly per
// (cg, id) so the model never has to infer them.
func (m *naiveModel) refs(id string) map[string]int {
	r := map[string]int{}
	for cg, mm := range m.maps {
		for _, pages := range mm {
			for _, p := range pages {
				if p.ID == id {
					r[cg]++
				}
			}
		}
	}
	return r
}

// residentPages returns the distinct set of page IDs that have at least one
// reference anywhere, together with their sizes.
func (m *naiveModel) residentPages() map[string]int64 {
	sz := map[string]int64{}
	for _, mm := range m.maps {
		for _, pages := range mm {
			for _, p := range pages {
				sz[p.ID] = p.Size
			}
		}
	}
	return sz
}

func (m *naiveModel) total() int64 {
	var n int64
	for _, s := range m.residentPages() {
		n += s
	}
	return n
}

func (m *naiveModel) hasLimit(cg string) bool {
	_, ok := m.limits[cg]
	return ok
}

func (m *naiveModel) setLimit(cg string, bytes int64) (err error, reason string) {
	if cg == "" || bytes < 0 || bytes > maxCap {
		return ErrParam, "SetLimit: empty cg or bytes out of range"
	}
	m.limits[cg] = bytes
	if m.maps[cg] == nil {
		m.maps[cg] = map[string][]Page{}
	}
	return nil, "SetLimit accepted: registered/updated limit"
}

// ownerOf computes the owner of id purely from references plus per-holder
// since values held by the model via sinceStore.
type naiveHolder struct {
	since int64
}

func bestHolderName(hold map[string]naiveHolder) string {
	type cand struct {
		name  string
		since int64
	}
	var cands []cand
	for name, h := range hold {
		cands = append(cands, cand{name, h.since})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].since != cands[j].since {
			return cands[i].since < cands[j].since
		}
		return cands[i].name < cands[j].name
	})
	return cands[0].name
}

// owners computes, for one hypothetical set of mappings and since values, the
// owner of every resident page.
func owners(mapsState map[string]map[string][]Page, since map[string]int64) map[string]string {
	type ref struct{}
	holders := map[string]map[string]naiveHolder{} // page -> cg -> holder
	pageSize := map[string]int64{}
	for cg, mm := range mapsState {
		for _, pages := range mm {
			for _, p := range pages {
				pageSize[p.ID] = p.Size
				if holders[p.ID] == nil {
					holders[p.ID] = map[string]naiveHolder{}
				}
				holders[p.ID][cg] = naiveHolder{since: since[cg+"\x00"+p.ID]}
			}
		}
	}
	out := map[string]string{}
	for id := range holders {
		out[id] = bestHolderName(holders[id])
	}
	return out
}

func (m *naiveModel) usedFrom(owner map[string]string, sz map[string]int64) map[string]int64 {
	u := map[string]int64{}
	for id, cg := range owner {
		u[cg] += sz[id]
	}
	return u
}

func (m *naiveModel) snapshot() (map[string]map[string][]Page, map[string]int64) {
	mapsCopy := map[string]map[string][]Page{}
	for cg, mm := range m.maps {
		mapsCopy[cg] = map[string][]Page{}
		for name, pages := range mm {
			cp := make([]Page, len(pages))
			copy(cp, pages)
			mapsCopy[cg][name] = cp
		}
	}
	sinceCopy := make(map[string]int64, len(m.since))
	for k, v := range m.since {
		sinceCopy[k] = v
	}
	return mapsCopy, sinceCopy
}

func (m *naiveModel) logical(cg string) int64 {
	distinct := map[string]int64{}
	for _, pages := range m.maps[cg] {
		for _, p := range pages {
			distinct[p.ID] = p.Size
		}
	}
	var n int64
	for _, s := range distinct {
		n += s
	}
	return n
}

func (m *naiveModel) over(cg string) bool {
	lim, ok := m.limits[cg]
	if !ok {
		return false
	}
	sz := m.residentPages()
	u := m.usedFrom(owners(m.maps, m.since), sz)
	return u[cg] > lim
}

// mapOp is a naive Map: it validates against a hypothetical post-op snapshot
// and commits only when every rule passes.
func (m *naiveModel) mapOp(now int64, cg, name string, pages []Page) (err error, reason string) {
	if name == "" || len(pages) == 0 || len(pages) > maxPages {
		return ErrParam, "Map: empty name or empty/too-long page sequence"
	}
	for _, p := range pages {
		if p.ID == "" || p.Size < 1 || p.Size > maxPageSz {
			return ErrParam, "Map: empty page ID or size out of range"
		}
	}
	if now < 0 {
		return ErrParam, "Map: negative clock"
	}
	if now < m.now {
		return ErrClock, "Map: now below last accepted clock"
	}
	if !m.hasLimit(cg) {
		return ErrNoLimit, "Map: cgroup never registered with SetLimit"
	}
	if _, ok := m.maps[cg][name]; ok {
		return ErrExists, "Map: same mapping name already exists in cgroup"
	}

	// Size consistency: first occurrence fixes the size for this request.
	first := map[string]int64{}
	for _, p := range pages {
		if sz, ok := first[p.ID]; ok {
			if sz != p.Size {
				return ErrSizeMismatch, "Map: duplicate ID with differing size within the request"
			}
		} else {
			first[p.ID] = p.Size
		}
	}
	resident := m.residentPages()
	for id, sz := range first {
		if old, ok := resident[id]; ok && old != sz {
			return ErrSizeMismatch, "Map: page size differs from resident page"
		}
	}

	// Build the hypothetical post-op state.
	hypMaps, hypSince := m.snapshot()
	stored := make([]Page, len(pages))
	copy(stored, pages)
	hypMaps[cg][name] = stored
	for id := range first {
		key := cg + "\x00" + id
		if _, held := m.refs(id)[cg]; !held {
			hypSince[key] = now
		}
	}

	hypPages := map[string]int64{}
	for _, mm := range hypMaps {
		for _, ps := range mm {
			for _, p := range ps {
				hypPages[p.ID] = p.Size
			}
		}
	}
	var fresh int64
	for id, sz := range first {
		if _, existed := resident[id]; !existed {
			fresh += sz
		}
	}
	var total int64
	for _, sz := range hypPages {
		total += sz
	}
	if total > m.cap {
		return ErrCapacity, "Map: total plus fresh pages exceeds global cap"
	}

	beforeOwner := owners(m.maps, m.since)
	afterOwner := owners(hypMaps, hypSince)
	beforeUsed := m.usedFrom(beforeOwner, resident)
	afterUsed := m.usedFrom(afterOwner, hypPages)
	delta := afterUsed[cg] - beforeUsed[cg]
	if delta > 0 && afterUsed[cg] > m.limits[cg] {
		return ErrLimit, "Map: cgroup gains ownership beyond its limit"
	}

	// Commit.
	m.maps = hypMaps
	m.since = hypSince
	m.now = now
	_ = delta
	return nil, "Map accepted: fresh and delta within capacity/limit"
}

func (m *naiveModel) unmapOp(now int64, cg, name string) (err error, reason string) {
	if cg == "" || name == "" || now < 0 {
		return ErrParam, "Unmap: empty cg/name or negative clock"
	}
	if now < m.now {
		return ErrClock, "Unmap: now below last accepted clock"
	}
	if !m.hasLimit(cg) {
		return ErrNoLimit, "Unmap: cgroup never registered"
	}
	if _, ok := m.maps[cg][name]; !ok {
		return ErrNotFound, "Unmap: mapping does not exist in cgroup"
	}

	// A cgroup whose refs of a page fall to zero loses its since; the next
	// reference starts a fresh streak.
	beforeRef := map[string]int{}
	for _, ps := range m.maps[cg] {
		for _, p := range ps {
			beforeRef[p.ID]++
		}
	}
	removed := map[string]int{}
	for _, p := range m.maps[cg][name] {
		removed[p.ID]++
	}
	for id, n := range removed {
		if beforeRef[id] == n {
			delete(m.since, cg+"\x00"+id)
		}
	}
	delete(m.maps[cg], name)
	m.now = now
	return nil, "Unmap accepted: references dropped, ownership migrated by rule"
}
