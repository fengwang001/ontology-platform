package pagecache

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// Naive reference model: it keeps only the raw inputs (limits and mappings)
// and recomputes references, holders, ownership and every counter from
// scratch after each accepted operation. The incremental Cache must always
// agree with it.

type naiveHolder struct {
	since int64
	ref   int64
}

type naiveMapping struct {
	now   int64
	pages []Page
}

type naiveModel struct {
	capacity int64
	now      int64
	limits   map[string]int64
	hasLimit map[string]bool
	mappings map[string]map[string]naiveMapping
	// refs[id][cg] tracks references and the most recent 0 -> positive time.
	refs map[string]map[string]*naiveHolder
	// size[id] is the cache-remembered size; absent once the page is reclaimed.
	size map[string]int64
}

func newNaive(capacity int64) *naiveModel {
	return &naiveModel{
		capacity: capacity,
		limits:   make(map[string]int64),
		hasLimit: make(map[string]bool),
		mappings: make(map[string]map[string]naiveMapping),
		refs:     make(map[string]map[string]*naiveHolder),
		size:     make(map[string]int64),
	}
}

func validParams(now int64, cg, name string, pages []Page) bool {
	if cg == "" || name == "" || now < 0 || len(pages) == 0 || len(pages) > 10000 {
		return false
	}
	for _, p := range pages {
		if p.ID == "" || p.Size < 1 || p.Size > 1e9 {
			return false
		}
	}
	return true
}

func (m *naiveModel) holder(id, cg string) *naiveHolder {
	byCg := m.refs[id]
	if byCg == nil {
		byCg = make(map[string]*naiveHolder)
		m.refs[id] = byCg
	}
	h := byCg[cg]
	if h == nil {
		h = &naiveHolder{}
		byCg[cg] = h
	}
	return h
}

// snapshot recomputes owners and every counter by scanning all holders.
func (m *naiveModel) snapshot() (owners map[string]string, used, logical map[string]int64, total int64) {
	owners = make(map[string]string)
	used = make(map[string]int64)
	logical = make(map[string]int64)
	for id, byCg := range m.refs {
		size := m.size[id]
		owner := ""
		var ownerSince int64
		for cg, h := range byCg {
			if h.ref == 0 {
				continue
			}
			logical[cg] += size
			if owner == "" || h.since < ownerSince || (h.since == ownerSince && cg < owner) {
				owner = cg
				ownerSince = h.since
			}
		}
		if owner != "" {
			owners[id] = owner
			used[owner] += size
			total += size
		}
	}
	return owners, used, logical, total
}

func (m *naiveModel) setLimit(cg string, bytes int64) error {
	if cg == "" || bytes < 0 || bytes > 1e15 {
		return ErrParam
	}
	m.limits[cg] = bytes
	m.hasLimit[cg] = true
	return nil
}

func (m *naiveModel) mapPages(now int64, cg, name string, pages []Page) error {
	if !validParams(now, cg, name, pages) {
		return ErrParam
	}
	firstSize := make(map[string]int64)
	for _, p := range pages {
		if s, ok := firstSize[p.ID]; ok && s != p.Size {
			return ErrSizeMismatch
		}
		firstSize[p.ID] = p.Size
	}
	if now < m.now {
		return ErrClock
	}
	if !m.hasLimit[cg] {
		return ErrNoLimit
	}
	if _, ok := m.mappings[cg][name]; ok {
		return ErrExists
	}
	for id, s := range firstSize {
		if cached, ok := m.size[id]; ok && cached != s {
			return ErrSizeMismatch
		}
	}

	_, used, _, total := m.snapshot()
	var freshSum, deltaSum int64
	for id, size := range firstSize {
		if _, exists := m.size[id]; !exists {
			freshSum += size
		}
		since := now
		if h := m.refs[id][cg]; h != nil && h.ref > 0 {
			since = h.since
		}
		preOwner := ""
		var preSince int64
		for other, h := range m.refs[id] {
			if h.ref == 0 {
				continue
			}
			if preOwner == "" || h.since < preSince || (h.since == preSince && other < preOwner) {
				preOwner = other
				preSince = h.since
			}
		}
		postOwner := cg
		postSince := since
		if preOwner != "" && (preSince < postSince || (preSince == postSince && preOwner < postOwner)) {
			postOwner = preOwner
		}
		if postOwner == cg && preOwner != cg {
			deltaSum += size
		}
	}
	if total+freshSum > m.capacity {
		return ErrCapacity
	}
	if deltaSum > 0 && used[cg]+deltaSum > m.limits[cg] {
		return ErrLimit
	}

	stored := make([]Page, len(pages))
	copy(stored, pages)
	if m.mappings[cg] == nil {
		m.mappings[cg] = make(map[string]naiveMapping)
	}
	m.mappings[cg][name] = naiveMapping{now: now, pages: stored}
	for _, p := range stored {
		if _, ok := m.size[p.ID]; !ok {
			m.size[p.ID] = p.Size
		}
		h := m.holder(p.ID, cg)
		if h.ref == 0 {
			h.since = now
		}
		h.ref++
	}
	m.now = now
	return nil
}

func (m *naiveModel) unmap(now int64, cg, name string) error {
	if cg == "" || name == "" || now < 0 {
		return ErrParam
	}
	if now < m.now {
		return ErrClock
	}
	if !m.hasLimit[cg] {
		return ErrNoLimit
	}
	mp, ok := m.mappings[cg][name]
	if !ok {
		return ErrNotFound
	}

	delete(m.mappings[cg], name)
	for _, p := range mp.pages {
		h := m.holder(p.ID, cg)
		h.ref--
		if h.ref == 0 {
			h.since = 0
		}
	}
	// Reclaim pages with no holders and forget their sizes.
	for id, byCg := range m.refs {
		alive := false
		for other, h := range byCg {
			if h.ref > 0 {
				alive = true
			} else if other != cg {
				// keep stale zero entries out of the way
			}
		}
		if !alive {
			delete(m.refs, id)
			delete(m.size, id)
		}
	}
	m.now = now
	return nil
}

// compareSnapshots verifies the incremental cache against the naive model and
// the global accounting invariants.
func compareSnapshots(t *testing.T, c *Cache, m *naiveModel) {
	t.Helper()
	owners, used, logical, total := m.snapshot()
	if got := c.Total(); got != total {
		t.Fatalf("Total: cache=%d model=%d", got, total)
	}
	var usedSum int64
	for cg := range m.hasLimit {
		if got := c.Used(cg); got != used[cg] {
			t.Fatalf("Used(%q): cache=%d model=%d", cg, got, used[cg])
		}
		if got := c.Logical(cg); got != logical[cg] {
			t.Fatalf("Logical(%q): cache=%d model=%d", cg, got, logical[cg])
		}
		over := used[cg] > m.limits[cg]
		if c.Over(cg) != over {
			t.Fatalf("Over(%q): cache=%v model=%v", cg, c.Over(cg), over)
		}
		usedSum += used[cg]
	}
	if usedSum != total {
		t.Fatalf("invariant sum(used)=%d != total=%d", usedSum, total)
	}
	// Every live page has exactly one owner, and it is a holder.
	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.pages) != len(owners) {
		t.Fatalf("live pages: cache=%d model=%d", len(c.pages), len(owners))
	}
	for id, pe := range c.pages {
		want, ok := owners[id]
		if !ok {
			t.Fatalf("page %q live in cache but not model", id)
		}
		if pe.owner != want {
			t.Fatalf("page %q owner: cache=%q model=%q", id, pe.owner, want)
		}
		if pe.holders[want] == nil || pe.holders[want].ref <= 0 {
			t.Fatalf("page %q owner %q is not a holder", id, want)
		}
		if pe.size != m.size[id] {
			t.Fatalf("page %q size: cache=%d model=%d", id, pe.size, m.size[id])
		}
	}
}

const randomSequences = 2000

// TestRandomAgainstNaiveModel replays 2000 random operation sequences on both
// the incremental cache and the naive model, requiring identical decisions
// and states after every step. Inputs, outputs and the decision rationale are
// logged.
func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for seq := 0; seq < randomSequences; seq++ {
		capacity := int64(1 + rng.Intn(500))
		cache, err := New(capacity)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		model := newNaive(capacity)
		cgNames := []string{"A", "B", "C"}

		var log []string
		log = append(log, fmt.Sprintf("seq=%d capacity=%d", seq, capacity))
		failf := func(format string, args ...any) {
			log = append(log, "MISMATCH: "+fmt.Sprintf(format, args...))
			for _, line := range log {
				t.Log(line)
			}
			t.Fatalf("seq %d mismatch (see log)", seq)
		}

		var now int64
		steps := 5 + rng.Intn(40)
		for step := 0; step < steps; step++ {
			cg := cgNames[rng.Intn(len(cgNames))]
			switch rng.Intn(10) {
			case 0, 1:
				limit := int64(rng.Intn(int(capacity) + 200))
				if errCache := cache.SetLimit(cg, limit); errCache != model.setLimit(cg, limit) {
					failf("SetLimit step=%d cg=%s limit=%d cache=%v model=%v", step, cg, limit, errCache, errCache)
				}
				log = append(log, fmt.Sprintf("step=%d SetLimit cg=%s bytes=%d -> ok", step, cg, limit))
			case 2, 3, 4, 5, 6, 7:
				// Mostly non-decreasing time, occasionally older to trigger ErrClock.
				if rng.Intn(8) == 0 && now > 0 {
					now = rng.Int63n(now)
				} else {
					now += int64(rng.Intn(4))
				}
				name := fmt.Sprintf("m%d", rng.Intn(6))
				n := 1 + rng.Intn(5)
				pages := make([]Page, n)
				poolSize := make(map[string]int64)
				for i := range pages {
					id := fmt.Sprintf("p%d", rng.Intn(5))
					size := int64(1 + rng.Intn(120))
					// Reuse a size for the same ID within this mapping sometimes.
					if s, ok := poolSize[id]; ok && rng.Intn(2) == 0 {
						size = s
					}
					poolSize[id] = size
					pages[i] = Page{ID: id, Size: size}
				}
				before := cache.Total()
				errCache := cache.Map(now, cg, name, pages)
				errModel := model.mapPages(now, cg, name, pages)
				log = append(log, fmt.Sprintf("step=%d Map now=%d cg=%s name=%s pages=%v totalBefore=%d -> %v | model %v",
					step, now, cg, name, pages, before, errCache, errModel))
				if !sameErr(errCache, errModel) {
					failf("Map step=%d now=%d cg=%s name=%s pages=%v cache=%v model=%v",
						step, now, cg, name, pages, errCache, errModel)
				}
			default:
				if rng.Intn(8) == 0 && now > 0 {
					now = rng.Int63n(now)
				} else {
					now += int64(rng.Intn(4))
				}
				name := fmt.Sprintf("m%d", rng.Intn(6))
				errCache := cache.Unmap(now, cg, name)
				errModel := model.unmap(now, cg, name)
				log = append(log, fmt.Sprintf("step=%d Unmap now=%d cg=%s name=%s -> %v | model %v",
					step, now, cg, name, errCache, errModel))
				if !sameErr(errCache, errModel) {
					failf("Unmap step=%d now=%d cg=%s name=%s cache=%v model=%v",
						step, now, cg, name, errCache, errModel)
				}
			}
			compareSnapshots(t, cache, model)
		}
		log = append(log, fmt.Sprintf("seq=%d completed: total=%d", seq, cache.Total()))
		if testing.Verbose() {
			for _, line := range log {
				t.Log(line)
			}
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b)
}
