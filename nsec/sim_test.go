package nsec

import (
	"fmt"
	"math/bits"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// simEntry / sim are a deliberately naive, linear-scan implementation of the
// specification, used as the reference model for randomized comparison.
type simEntry struct {
	owner  name
	next   name
	types  map[uint16]bool
	expiry uint64
	rec    Record
}

type sim struct {
	zone   name
	soaMin uint32
	cap    int
	recs   map[string]*simEntry
	last   uint64
}

func newSim(zone string, soaMin uint32, capacity int) *sim {
	z, _ := parseName(zone)
	return &sim{zone: z, soaMin: soaMin, cap: capacity, recs: map[string]*simEntry{}}
}

func (s *sim) purge(now uint64) {
	for k, e := range s.recs {
		if e.expiry <= now {
			delete(s.recs, k)
		}
	}
}

func (s *sim) insert(now uint64, rec Record) error {
	owner, okOwner := parseName(rec.Owner)
	next, okNext := parseName(rec.Next)
	if now > maxNow || !okOwner || !okNext || rec.TTL > maxTTL || !hasNSEC(rec.Types) {
		return ErrInvalidParam
	}
	if now < s.last {
		return ErrClockRegression
	}
	if !rec.Validated {
		return ErrNotValidated
	}
	if !owner.inZone(s.zone) || !next.inZone(s.zone) {
		return ErrOutOfZone
	}
	s.last = now
	s.purge(now)
	delete(s.recs, owner.String())
	eff := rec.TTL
	if s.soaMin < eff {
		eff = s.soaMin
	}
	if eff == 0 {
		return nil
	}
	if len(s.recs) >= s.cap {
		victim := ""
		var ve *simEntry
		for k, e := range s.recs {
			if ve == nil || e.expiry < ve.expiry ||
				(e.expiry == ve.expiry &&
					compareLabels(e.owner.labels, ve.owner.labels) < 0) {
				victim, ve = k, e
			}
		}
		delete(s.recs, victim)
	}
	types := map[uint16]bool{}
	for _, tp := range rec.Types {
		types[tp] = true
	}
	s.recs[owner.String()] = &simEntry{
		owner:  owner,
		next:   next,
		types:  types,
		expiry: now + uint64(eff),
		rec: Record{
			Owner:     owner.String(),
			Next:      next.String(),
			Types:     append([]uint16(nil), rec.Types...),
			TTL:       rec.TTL,
			Validated: rec.Validated,
		},
	}
	return nil
}

// simCovers is the specification's coverage rule evaluated directly.
func simCovers(e *simEntry, x name) bool {
	if compareLabels(e.owner.labels, e.next.labels) < 0 {
		return compareLabels(e.owner.labels, x.labels) < 0 &&
			compareLabels(x.labels, e.next.labels) < 0
	}
	return compareLabels(x.labels, e.owner.labels) > 0 ||
		compareLabels(x.labels, e.next.labels) < 0
}

// simCovering picks the covering record with the canonically largest owner.
func (s *sim) simCovering(x name) *simEntry {
	var best *simEntry
	for _, e := range s.recs {
		if simCovers(e, x) &&
			(best == nil || compareLabels(e.owner.labels, best.owner.labels) > 0) {
			best = e
		}
	}
	return best
}

// lookup mirrors the specification step by step and also returns a
// human-readable justification for logging.
func (s *sim) lookup(now uint64, qname string, qtype uint32) (Result, error, string) {
	qn, ok := parseName(qname)
	if now > maxNow || !ok || qtype < 1 || qtype > 65535 {
		return Result{}, ErrInvalidParam, "invalid parameter"
	}
	if now < s.last {
		return Result{}, ErrClockRegression, "clock regression"
	}
	if !qn.inZone(s.zone) {
		return Result{}, ErrOutOfZone, "qname out of zone"
	}
	s.last = now
	s.purge(now)
	qt := uint16(qtype)
	if e, hit := s.recs[qn.String()]; hit {
		if e.types[qt] || (qt != TypeCNAME && e.types[TypeCNAME]) {
			return Result{Kind: Miss}, nil,
				fmt.Sprintf("owner %s exists with matching type/CNAME", qn)
		}
		return Result{Kind: NoData, Used: []Record{e.rec}, TTL: uint32(e.expiry - now)}, nil,
			fmt.Sprintf("owner %s exists without type %d", qn, qt)
	}
	r := s.simCovering(qn)
	if r == nil {
		return Result{Kind: Miss}, nil, "no record covers qname"
	}
	k := commonSuffix(r.owner.labels, qn.labels)
	if k2 := commonSuffix(r.next.labels, qn.labels); k2 > k {
		k = k2
	}
	if k == len(qn.labels) {
		return Result{Kind: NoData, Used: []Record{r.rec}, TTL: uint32(r.expiry - now)}, nil,
			fmt.Sprintf("empty non-terminal: covered by %s, k=%d", r.owner, k)
	}
	wild := name{labels: append([]string{"*"}, qn.labels[len(qn.labels)-k:]...)}
	if _, hit := s.recs[wild.String()]; hit {
		return Result{Kind: Miss}, nil,
			fmt.Sprintf("wildcard %s exists", wild)
	}
	w := s.simCovering(wild)
	if w == nil {
		return Result{Kind: Miss}, nil,
			fmt.Sprintf("wildcard %s not covered", wild)
	}
	used := []*simEntry{r}
	if w != r {
		used = append(used, w)
	}
	sort.Slice(used, func(i, j int) bool {
		return compareLabels(used[i].owner.labels, used[j].owner.labels) < 0
	})
	recs := make([]Record, len(used))
	ttl := ^uint64(0)
	for i, e := range used {
		recs[i] = e.rec
		if rem := e.expiry - now; rem < ttl {
			ttl = rem
		}
	}
	return Result{Kind: NXDomain, Used: recs, TTL: uint32(ttl)}, nil,
		fmt.Sprintf("covered by %s, k=%d, wildcard %s covered by %s",
			r.owner, k, wild, w.owner)
}

// randName draws names from a pool that covers the zone apex, descendants
// of depth 1-3, wildcards, out-of-zone names and malformed names.
func randName(r *rand.Rand) string {
	labels := []string{"a", "b", "c", "d", "e", "f", "g", "h", "*", "x-y", "z_z", "0", "ww"}
	switch r.Intn(20) {
	case 0:
		return "example"
	case 1:
		return "other.com"
	case 2:
		return "a.other.com"
	case 3:
		return "com"
	case 4:
		return "bad..name"
	case 5:
		return ""
	}
	depth := 1 + r.Intn(3)
	parts := make([]string, 0, depth+1)
	for i := 0; i < depth; i++ {
		parts = append(parts, labels[r.Intn(len(labels))])
	}
	parts = append(parts, "example")
	out := parts[0]
	for _, p := range parts[1:] {
		out += "." + p
	}
	if r.Intn(10) == 0 {
		out += "."
	}
	return out
}

func randTypes(r *rand.Rand) []uint16 {
	pool := []uint16{1, 2, 5, 6, 15, 16, 47, 48}
	var out []uint16
	for _, tp := range pool {
		if r.Intn(3) == 0 {
			out = append(out, tp)
		}
	}
	if r.Intn(2) == 0 {
		out = append(out, TypeNSEC)
	}
	return out
}

// TestRandomAgainstSim replays 2000 random operation sequences against both
// the cache and the naive model, logging inputs, outputs and the decision
// basis for every operation.
func TestRandomAgainstSim(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		soaMin := []uint32{0, 1, 7, 200, 86400}[r.Intn(5)]
		capacity := 1 + r.Intn(8)
		c, err := New("example", soaMin, capacity)
		if err != nil {
			t.Fatalf("seq %d: New: %v", seq, err)
		}
		s := newSim("example", soaMin, capacity)
		now := uint64(r.Intn(100))
		ops := 20 + r.Intn(15)
		for op := 0; op < ops; op++ {
			step := uint64(r.Intn(60))
			if r.Intn(20) == 0 && now > 50 {
				now -= uint64(r.Intn(50)) // exercise clock regression
			} else {
				now += step
			}
			if r.Intn(2) == 0 {
				rec := Record{
					Owner:     randName(r),
					Next:      randName(r),
					Types:     randTypes(r),
					TTL:       uint32(r.Intn(400)),
					Validated: r.Intn(10) != 0,
				}
				if r.Intn(40) == 0 {
					rec.TTL = 86401 // invalid
				}
				gotErr := c.Insert(now, rec)
				wantErr := s.insert(now, rec)
				t.Logf("seq=%d op=%d Insert(now=%d owner=%q next=%q types=%v ttl=%d validated=%v) => err=%v (model err=%v)",
					seq, op, now, rec.Owner, rec.Next, rec.Types, rec.TTL, rec.Validated, gotErr, wantErr)
				if gotErr != wantErr {
					t.Fatalf("seq %d op %d Insert: cache err %v, model err %v",
						seq, op, gotErr, wantErr)
				}
			} else {
				qname := randName(r)
				qtype := uint32(1 + r.Intn(65535))
				switch r.Intn(30) {
				case 0:
					qtype = 0
				case 1:
					qtype = 65536
				}
				got, gotErr := c.Lookup(now, qname, qtype)
				want, wantErr, why := s.lookup(now, qname, qtype)
				t.Logf("seq=%d op=%d Lookup(now=%d qname=%q qtype=%d) => %v used=%v ttl=%d err=%v (basis: %s)",
					seq, op, now, qname, qtype, got.Kind, usedOwners(got), got.TTL, gotErr, why)
				if gotErr != wantErr {
					t.Fatalf("seq %d op %d Lookup(%q,%d): cache err %v, model err %v",
						seq, op, qname, qtype, gotErr, wantErr)
				}
				if gotErr != nil {
					continue
				}
				if got.Kind != want.Kind || got.TTL != want.TTL ||
					!reflect.DeepEqual(usedOwners(got), usedOwners(want)) {
					t.Fatalf("seq %d op %d Lookup(%q,%d):\ncache: %v %v ttl=%d\nmodel: %v %v ttl=%d",
						seq, op, qname, qtype,
						got.Kind, usedOwners(got), got.TTL,
						want.Kind, usedOwners(want), want.TTL)
				}
			}
		}
	}
}

// TestReplayDeterminism runs identical operation sequences against two
// caches and requires identical outcomes.
func TestReplayDeterminism(t *testing.T) {
	for seq := 0; seq < 50; seq++ {
		r := rand.New(rand.NewSource(int64(seq)*104729 + 7))
		c1 := mustNew(t, "example", 200, 8)
		c2 := mustNew(t, "example", 200, 8)
		now := uint64(0)
		for op := 0; op < 40; op++ {
			now += uint64(r.Intn(30))
			if r.Intn(2) == 0 {
				rec := Record{
					Owner:     randName(r),
					Next:      randName(r),
					Types:     randTypes(r),
					TTL:       uint32(r.Intn(300)),
					Validated: true,
				}
				if e1, e2 := c1.Insert(now, rec), c2.Insert(now, rec); e1 != e2 {
					t.Fatalf("seq %d op %d: insert errors differ: %v vs %v", seq, op, e1, e2)
				}
			} else {
				qname, qtype := randName(r), uint32(1+r.Intn(65535))
				res1, e1 := c1.Lookup(now, qname, qtype)
				res2, e2 := c2.Lookup(now, qname, qtype)
				if e1 != e2 || !reflect.DeepEqual(res1, res2) {
					t.Fatalf("seq %d op %d: %v/%v vs %v/%v", seq, op, res1, e1, res2, e2)
				}
			}
		}
	}
}

// TestNameCmpBound asserts the Lookup comparison budget
// 4*(ceil(log2(n+1))+2) on non-overlapping chains with one wrap record.
func TestNameCmpBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		c := mustNew(t, "example", 86400, 1)
		c.cap = n // white-box: the spec caps N at 4096 but the bound is at n=10000
		for i := 0; i < n; i++ {
			next := fmt.Sprintf("n%06d.example", i+1)
			if i == n-1 {
				next = "example" // single wrap record closes the chain
			}
			mustInsert(t, c, 0, Record{
				Owner: fmt.Sprintf("n%06d.example", i), Next: next,
				Types: []uint16{TypeNSEC}, TTL: 86400, Validated: true,
			})
		}
		if c.overlap {
			t.Fatalf("n=%d: chain unexpectedly flagged overlapping", n)
		}
		bound := uint64(4 * (bits.Len(uint(n)) + 2)) // ceil(log2(n+1)) == bits.Len(n)
		queries := []string{
			"n000000.example", // exact owner
			"n" + fmt.Sprintf("%06d", n/2) + ".example", // exact owner mid-chain
			"zz.n000000.example",                        // covered, wildcard covered -> NXDomain
			"zz.n005000.example",                        // covered mid-chain -> NXDomain (2 searches)
			"aaa.example",                               // gap between apex and first owner -> Miss
			"zzz.example",                               // beyond last owner -> covered by wrap
			"a.b.n000100.example",                       // deep name, wildcard below owner
		}
		if n < 5001 {
			queries[3] = "zz.n000050.example"
		}
		for _, q := range queries {
			c.nameCmp = 0
			res, err := c.Lookup(0, q, 1)
			if err != nil {
				t.Fatalf("n=%d q=%q: %v", n, q, err)
			}
			if c.nameCmp > bound {
				t.Fatalf("n=%d q=%q kind=%v: nameCmp=%d exceeds bound %d",
					n, q, res.Kind, c.nameCmp, bound)
			}
			t.Logf("n=%d q=%q kind=%v nameCmp=%d bound=%d", n, q, res.Kind, c.nameCmp, bound)
		}
	}
}

// TestConcurrentAccess hammers the cache from many goroutines; run with
// -race. Results must be error-free since the clock only moves forward.
func TestConcurrentAccess(t *testing.T) {
	c := mustNew(t, "example", 200, 64)
	var clock uint64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 200; i++ {
				now := atomic.AddUint64(&clock, 1)
				if r.Intn(2) == 0 {
					_ = c.Insert(now, Record{
						Owner:     fmt.Sprintf("h%02d.example", r.Intn(40)),
						Next:      "example",
						Types:     []uint16{1, TypeNSEC},
						TTL:       uint32(1 + r.Intn(300)),
						Validated: true,
					})
				} else {
					_, _ = c.Lookup(now, fmt.Sprintf("h%02d.example", r.Intn(40)), 1)
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestPurgeRemovesExpired verifies that accepted operations clear expired
// records exactly at their expiration second.
func TestPurgeRemovesExpired(t *testing.T) {
	c := mustNew(t, "example", 200, 4)
	mustInsert(t, c, 100, Record{
		Owner: "a.example", Next: "b.example",
		Types: []uint16{TypeNSEC}, TTL: 50, Validated: true, // expires 150
	})
	mustInsert(t, c, 100, Record{
		Owner: "c.example", Next: "d.example",
		Types: []uint16{TypeNSEC}, TTL: 100, Validated: true, // expires 200
	})
	mustLookup(t, c, 149, "a.example", 2)
	if got := len(c.sorted); got != 2 {
		t.Fatalf("at 149: %d records, want 2", got)
	}
	mustLookup(t, c, 150, "a.example", 2)
	if got := len(c.sorted); got != 1 {
		t.Fatalf("at 150: %d records, want 1", got)
	}
	mustLookup(t, c, 200, "c.example", 2)
	if got := len(c.sorted); got != 0 {
		t.Fatalf("at 200: %d records, want 0", got)
	}
}
