package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/key"
)

// ---- Naive reference model (single-threaded, spec written literally) ----

type mEntry struct {
	path, owner string
	names, vals []string
	public      bool
	status      int
	storedAt    int64
	expireAt    int64
	size        int64
	seq         int64
}

type model struct {
	cap     int64
	v       int
	pols    []policy
	entries map[string][]*mEntry
	bytes   int64
	seq     int64
	maxNow  int64
	fetches int
	evicted int
	hits    int
	misses  int
	bypass  int
}

func newModel(capBytes int64, v int) *model {
	return &model{cap: capBytes, v: v, entries: map[string][]*mEntry{}}
}

type mOutcome struct {
	status int
	vis    Visibility
	ttl    int64
	size   int64
	vary   []string
	err    error
}

func (m *model) scopeOf(path string) string {
	best := ""
	for _, p := range m.pols {
		if len(p.prefix) > len(best) && segmentMatch(path, p.prefix) {
			best = p.prefix
		}
	}
	for _, p := range m.pols {
		if p.prefix == best {
			return p.scope
		}
	}
	return ""
}

func (m *model) run(req Request, now int64, o mOutcome) (Source, error) {
	if err := validateRequest(req); err != nil {
		return 0, err
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return 0, errInvalidTime
	}
	if now < m.maxNow {
		return 0, errClockSkew
	}
	if need := m.scopeOf(req.Path); need != "" && !containsScope(req.Scopes, need) {
		return 0, errForbidden
	}
	m.maxNow = now

	if req.Method != "GET" {
		m.fetches++
		if o.err != nil {
			return 0, o.err
		}
		if o.vis < Public || o.vis > NoStore || o.ttl < 0 || o.size < 0 {
			return 0, errInvalidResponse
		}
		m.bypass++
		if o.status >= 200 && o.status <= 399 {
			for _, e := range m.entries[req.Path] {
				m.bytes -= e.size
			}
			delete(m.entries, req.Path)
		}
		return Bypass, nil
	}

	// lookup
	var best *mEntry
	for _, e := range m.entries[req.Path] {
		if now >= e.expireAt {
			continue
		}
		if e.owner != "" && (req.Subject == "" || e.owner != req.Subject) {
			continue
		}
		id := key.Identity{Owner: e.owner, Names: e.names, Vals: e.vals}
		if !id.Matches(req.Headers) {
			continue
		}
		if best == nil {
			best = e
			continue
		}
		cp, bp := e.owner != "", best.owner != ""
		if cp != bp {
			if cp {
				best = e
			}
			continue
		}
		if e.storedAt != best.storedAt {
			if e.storedAt > best.storedAt {
				best = e
			}
			continue
		}
		if e.seq > best.seq {
			best = e
		}
	}
	if best != nil {
		m.hits++
		return Hit, nil
	}
	m.fetches++
	if o.err != nil {
		return 0, o.err
	}
	if o.vis < Public || o.vis > NoStore || o.ttl < 0 || o.size < 0 {
		return 0, errInvalidResponse
	}
	m.misses++

	// store rules
	canStore := (o.vis == Public || (o.vis == Private && req.Subject != "")) &&
		o.status >= 200 && o.status <= 299 && o.ttl > 0 && o.size <= m.cap
	for _, vv := range o.vary {
		if vv == "*" {
			canStore = false
		}
	}
	if canStore {
		owner := ""
		if o.vis == Private {
			owner = req.Subject
		}
		id := key.Build(owner, o.vary, req.Headers)
		ne := &mEntry{
			path: req.Path, owner: owner, names: id.Names, vals: id.Vals,
			public: o.vis == Public, status: o.status, storedAt: now,
			expireAt: now + o.ttl, size: o.size,
		}
		list := m.entries[req.Path]
		kept := list[:0]
		for _, old := range list {
			oid := key.Identity{Owner: old.owner, Names: old.names, Vals: old.vals}
			if oid.Equal(id) {
				m.bytes -= old.size
				continue
			}
			kept = append(kept, old)
		}
		m.seq++
		ne.seq = m.seq
		kept = append(kept, ne)
		m.bytes += ne.size
		for len(kept) > m.v {
			vi := 0
			for i := 1; i < len(kept); i++ {
				if kept[i].storedAt < kept[vi].storedAt ||
					(kept[i].storedAt == kept[vi].storedAt && kept[i].seq < kept[vi].seq) {
					vi = i
				}
			}
			m.bytes -= kept[vi].size
			m.evicted++
			kept = append(kept[:vi], kept[vi+1:]...)
		}
		m.entries[req.Path] = kept
		for m.bytes > m.cap {
			var vp string
			var vi int
			var vic *mEntry
			for p, es := range m.entries {
				for i, cand := range es {
					if vic == nil || cand.expireAt < vic.expireAt ||
						(cand.expireAt == vic.expireAt && cand.seq < vic.seq) {
						vic, vp, vi = cand, p, i
					}
				}
			}
			es := m.entries[vp]
			m.bytes -= vic.size
			m.evicted++
			es = append(es[:vi], es[vi+1:]...)
			if len(es) == 0 {
				delete(m.entries, vp)
			} else {
				m.entries[vp] = es
			}
		}
	}
	return Miss, nil
}

func (m *model) snapshot() string {
	var b strings.Builder
	fmt.Fprintf(&b, "bytes=%d fetches=%d evicted=%d hits=%d misses=%d bypass=%d\n",
		m.bytes, m.fetches, m.evicted, m.hits, m.misses, m.bypass)
	paths := make([]string, 0, len(m.entries))
	for p := range m.entries {
		paths = append(paths, p)
	}
	sortStrings(paths)
	for _, p := range paths {
		var seqs []int64
		for _, e := range m.entries[p] {
			seqs = append(seqs, e.seq)
		}
		sortInts(seqs)
		for _, sq := range seqs {
			for _, e := range m.entries[p] {
				if e.seq != sq {
					continue
				}
				fmt.Fprintf(&b, "  %s owner=%q names=%v vals=%v exp=%d size=%d\n",
					e.path, e.owner, e.names, e.vals, e.expireAt, e.size)
			}
		}
	}
	return b.String()
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}
func sortInts(a []int64) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j-1] > a[j]; j-- {
			a[j-1], a[j] = a[j], a[j-1]
		}
	}
}

// ---- Random differential test ----

type simStep struct {
	req Request
	now int64
	out mOutcome
	add *policy // non-nil: AddPolicy step
}

func TestRandomDifferential(t *testing.T) {
	const seeds = 2000
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		capBytes := int64(1 + rng.Intn(150))
		v := 1 + rng.Intn(4)
		c := New(capBytes, v)
		m := newModel(capBytes, v)

		paths := []string{"/a", "/b", "/admin", "/admin/x", "/adminx", "/me", "/p/q"}
		subjs := []string{"", "", "u1", "u2"}
		hdrs := []string{"", "en", "fr", "x"}
		var log []string
		fail := func(msg string) {
			t.Fatalf("seed=%d %s\nsteps:\n%s\n--- real ---\n%s\n--- model ---\n%s",
				seed, msg, strings.Join(log, "\n"), realState(c), m.snapshot())
		}

		nsteps := 8 + rng.Intn(25)
		now := int64(0)
		for st := 0; st < nsteps; st++ {
			step := simStep{}
			if rng.Intn(7) == 0 {
				if rng.Intn(5) == 0 {
					// Invalid AddPolicy: rejected, no state change.
					badPref := []string{"admin", ""}[rng.Intn(2)]
					scope := "s"
					errC := c.AddPolicy(badPref, scope)
					if !errors.Is(errC, errInvalidArgument) {
						t.Fatalf("unexpected AddPolicy err %v", errC)
					}
					log = append(log, fmt.Sprintf("AddPolicy(%q,%q) err=%v", badPref, scope, errC))
					continue
				}
				pref := []string{"/admin", "/admin/secret", "/x"}[rng.Intn(3)]
				scope := []string{"admin", "secret", "s"}[rng.Intn(3)]
				if err := c.AddPolicy(pref, scope); err != nil {
					t.Fatalf("valid AddPolicy failed: %v", err)
				}
				replaced := false
				for i := range m.pols {
					if m.pols[i].prefix == pref {
						m.pols[i].scope = scope
						replaced = true
					}
				}
				if !replaced {
					m.pols = append(m.pols, policy{prefix: pref, scope: scope})
				}
				log = append(log, fmt.Sprintf("AddPolicy(%q,%q) err=<nil>", pref, scope))
				continue
			}

			path := paths[rng.Intn(len(paths))]
			method := "GET"
			if rng.Intn(4) == 0 {
				method = []string{"POST", "PUT", "DELETE", "WAT"}[rng.Intn(4)]
			}
			subj := subjs[rng.Intn(len(subjs))]
			hmap := map[string]string{}
			if v := hdrs[rng.Intn(len(hdrs))]; v != "" {
				hmap["accept-language"] = v
			}
			if rng.Intn(3) == 0 {
				hmap["authorization"] = "tok"
			}
			scopes := []string(nil)
			if rng.Intn(2) == 0 {
				scopes = []string{"admin"}
			}
			// mostly monotonic time; sometimes equal; sometimes backward
			switch rng.Intn(10) {
			case 0:
			case 1:
				if now > 0 {
					step.now = now - 1
				} else {
					step.now = now
				}
			default:
				step.now = now + int64(rng.Intn(6))
			}
			if step.now >= now {
				now = step.now
			}
			if rng.Intn(40) == 0 {
				step.now = -1 // invalid range occasionally
			}
			step.req = Request{Method: method, Path: path, Subject: subj, Scopes: scopes, Headers: hmap}

			// scripted origin outcome for this step
			step.out = rollOutcome(rng, method)

			fetch := func(_ context.Context, req Request) (FetchResult, error) {
				if step.out.err != nil {
					return FetchResult{}, step.out.err
				}
				return FetchResult{
					Status: step.out.status, Visibility: step.out.vis,
					TTLMillis: step.out.ttl, Vary: append([]string(nil), step.out.vary...),
					Size: step.out.size, Body: []byte("z"),
				}, nil
			}
			_, src, errR := c.Get(context.Background(), step.req, step.now, fetch)
			msrc, errM := m.run(step.req, step.now, step.out)
			log = append(log, fmt.Sprintf(
				"%s %s subj=%q scopes=%v hdr=%v now=%d -> src=%s err=%v | fetch(vis=%d status=%d ttl=%d size=%d vary=%v err=%v)",
				method, path, subj, scopes, hmap, step.now, src, errR,
				step.out.vis, step.out.status, step.out.ttl, step.out.size, step.out.vary, step.out.err))

			if (errR == nil) != (errM == nil) ||
				(errR != nil && errM != nil && !sameErr(errR, errM)) {
				fail(fmt.Sprintf("error mismatch real=%v model=%v", errR, errM))
			}
			if errR == nil && src != msrc {
				fail(fmt.Sprintf("source mismatch real=%s model=%s", src, msrc))
			}
			if c.Bytes() != m.bytes || c.StatsSnapshot().Fetches != m.fetches ||
				c.StatsSnapshot().Evicted != m.evicted {
				fail("counters mismatch")
			}
			if c.StatsSnapshot().Hits != m.hits || c.StatsSnapshot().Misses != m.misses ||
				c.StatsSnapshot().Bypasses != m.bypass {
				fail("source counters mismatch")
			}
			assertEntries(t, c, m, seed, log)
			if c.Bytes() > capBytes {
				fail("invariant: bytes > Cap")
			}
			for p := range m.entries {
				if c.PathVariants(p) > v {
					fail("invariant: per-path variants > V")
				}
			}
		}
	}
}

func rollOutcome(rng *rand.Rand, method string) mOutcome {
	o := mOutcome{}
	switch rng.Intn(12) {
	case 0:
		o.err = errBoom
		return o
	case 1:
		o.ttl = -1 // invalid response
	}
	o.vis = []Visibility{Public, Public, Public, Private, Private, NoStore}[rng.Intn(6)]
	if method != "GET" {
		o.status = []int{200, 204, 301, 400, 500}[rng.Intn(5)]
		o.ttl = int64(1 + rng.Intn(30))
		o.size = int64(1 + rng.Intn(120))
		return o
	}
	o.status = []int{200, 201, 299, 301, 404, 500}[rng.Intn(6)]
	o.ttl = int64(rng.Intn(31)) // may be 0
	o.size = int64(1 + rng.Intn(160))
	if rng.Intn(6) == 0 {
		o.vary = []string{"*"}
	} else if rng.Intn(2) == 0 {
		o.vary = []string{"accept-language"}
	}
	return o
}

func sameErr(a, b error) bool {
	switch {
	case errors.Is(a, errInvalidArgument) && errors.Is(b, errInvalidArgument):
		return true
	case errors.Is(a, errInvalidTime) && errors.Is(b, errInvalidTime):
		return true
	case errors.Is(a, errClockSkew) && errors.Is(b, errClockSkew):
		return true
	case errors.Is(a, errForbidden) && errors.Is(b, errForbidden):
		return true
	case errors.Is(a, errInvalidResponse) && errors.Is(b, errInvalidResponse):
		return true
	case errors.Is(a, errBoom) && errors.Is(b, errBoom):
		return true
	default:
		return a.Error() == b.Error()
	}
}

func assertEntries(t *testing.T, c *Cache, m *model, seed int64, log []string) {
	t.Helper()
	got := c.Entries()
	want := 0
	for _, es := range m.entries {
		want += len(es)
	}
	if len(got) != want {
		t.Fatalf("seed=%d entry count real=%d model=%d\nsteps:\n%s\n--- real ---\n%s\n--- model ---\n%s",
			seed, len(got), want, strings.Join(log, "\n"), realState(c), m.snapshot())
	}
	wantSet := map[string]bool{}
	for _, es := range m.entries {
		for _, e := range es {
			k := fmt.Sprintf("%s|%q|%v|%v|exp=%d|size=%d", e.path, e.owner, e.names, e.vals, e.expireAt, e.size)
			wantSet[k] = true
		}
	}
	for _, e := range got {
		k := fmt.Sprintf("%s|%q|%v|%v|exp=%d|size=%d", e.Path, e.Owner, e.Names, e.Vals, e.ExpireAt, e.Size)
		if !wantSet[k] {
			t.Fatalf("seed=%d unexpected real entry %s\nsteps:\n%s\n--- real ---\n%s\n--- model ---\n%s",
				seed, k, strings.Join(log, "\n"), realState(c), m.snapshot())
		}
	}
}

func realState(c *Cache) string {
	var b strings.Builder
	st := c.StatsSnapshot()
	fmt.Fprintf(&b, "bytes=%d fetches=%d evicted=%d hits=%d misses=%d bypass=%d\n",
		c.Bytes(), st.Fetches, st.Evicted, st.Hits, st.Misses, st.Bypasses)
	for _, e := range c.Entries() {
		fmt.Fprintf(&b, "  %s owner=%q names=%v vals=%v exp=%d size=%d\n",
			e.Path, e.Owner, e.Names, e.Vals, e.ExpireAt, e.Size)
	}
	return b.String()
}
