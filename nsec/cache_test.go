package nsec

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, zone string, soaMin uint32, cap int) *Cache {
	t.Helper()
	c, err := New(zone, soaMin, cap)
	if err != nil {
		t.Fatalf("New(%q, %d, %d): %v", zone, soaMin, cap, err)
	}
	return c
}

func mustInsert(t *testing.T, c *Cache, now uint64, rec Record) {
	t.Helper()
	if err := c.Insert(now, rec); err != nil {
		t.Fatalf("Insert(now=%d, %+v): %v", now, rec, err)
	}
}

func mustLookup(t *testing.T, c *Cache, now uint64, qname string, qtype uint32) Result {
	t.Helper()
	res, err := c.Lookup(now, qname, qtype)
	if err != nil {
		t.Fatalf("Lookup(now=%d, %q, %d): %v", now, qname, qtype, err)
	}
	return res
}

func usedOwners(res Result) []string {
	owners := make([]string, len(res.Used))
	for i, r := range res.Used {
		owners[i] = r.Owner
	}
	return owners
}

func checkResult(t *testing.T, res Result, kind Kind, owners []string, ttl uint32) {
	t.Helper()
	if res.Kind != kind {
		t.Fatalf("kind = %v, want %v", res.Kind, kind)
	}
	if got := usedOwners(res); !reflect.DeepEqual(got, owners) {
		t.Fatalf("used owners = %v, want %v", got, owners)
	}
	if res.TTL != ttl {
		t.Fatalf("ttl = %d, want %d", res.TTL, ttl)
	}
}

// The worked example from the specification: zone=example, soaMin=200.
// R1: example -> d.example, Types={SOA(6),NS(2),NSEC}, inserted at
// now=1000 with TTL=300 (eff=200, expires at 1200).
// R2: d.example -> example, Types={A(1),NSEC}, inserted at now=1100
// with TTL=100 (eff=100, expires at 1200).
func exampleCache(t *testing.T) *Cache {
	t.Helper()
	c := mustNew(t, "example", 200, 16)
	mustInsert(t, c, 1000, Record{
		Owner: "example", Next: "d.example",
		Types: []uint16{6, 2, TypeNSEC}, TTL: 300, Validated: true,
	})
	mustInsert(t, c, 1100, Record{
		Owner: "d.example", Next: "example",
		Types: []uint16{1, TypeNSEC}, TTL: 100, Validated: true,
	})
	return c
}

func TestExampleNXDomainSameRecord(t *testing.T) {
	c := exampleCache(t)
	// b.example is covered by R1; ce=example; *.example is also covered
	// by R1 ('*' < 'd'), so R1 alone proves NXDOMAIN.
	res := mustLookup(t, c, 1150, "b.example", 1)
	checkResult(t, res, NXDomain, []string{"example"}, 50)
}

func TestExampleNXDomainTwoRecords(t *testing.T) {
	c := exampleCache(t)
	// e.example is covered by the wrap record R2 (e.example > d.example);
	// *.example is covered by R1. Used sorted by canonical owner:
	// example < d.example.
	res := mustLookup(t, c, 1150, "e.example", 1)
	checkResult(t, res, NXDomain, []string{"example", "d.example"}, 50)
}

func TestExampleNXDomainBelowOwner(t *testing.T) {
	c := exampleCache(t)
	// z.d.example is covered by R2; k=2, ce=d.example; *.d.example is
	// also covered by R2.
	res := mustLookup(t, c, 1150, "z.d.example", 1)
	checkResult(t, res, NXDomain, []string{"d.example"}, 50)
}

func TestExampleNoDataAndMiss(t *testing.T) {
	c := exampleCache(t)
	// d.example exists with Types={A,NSEC}: MX is provably absent...
	res := mustLookup(t, c, 1150, "d.example", 15)
	checkResult(t, res, NoData, []string{"d.example"}, 50)
	// ...but A is present, so the cache cannot answer negatively.
	res = mustLookup(t, c, 1150, "d.example", 1)
	checkResult(t, res, Miss, []string{}, 0)
}

func TestExampleCNAMEAlias(t *testing.T) {
	c := mustNew(t, "example", 200, 16)
	mustInsert(t, c, 100, Record{
		Owner: "www.example", Next: "example",
		Types: []uint16{TypeCNAME, TypeNSEC}, TTL: 100, Validated: true,
	})
	// Types contain CNAME(5): any non-5 qtype is a Miss.
	res := mustLookup(t, c, 150, "www.example", 1)
	checkResult(t, res, Miss, []string{}, 0)
	// qtype == CNAME itself matches Types: also a Miss.
	res = mustLookup(t, c, 150, "www.example", 5)
	checkResult(t, res, Miss, []string{}, 0)

	// Without CNAME in Types, qtype=5 is ordinary NODATA.
	c2 := mustNew(t, "example", 200, 16)
	mustInsert(t, c2, 100, Record{
		Owner: "www.example", Next: "example",
		Types: []uint16{1, TypeNSEC}, TTL: 100, Validated: true,
	})
	res = mustLookup(t, c2, 150, "www.example", 5)
	checkResult(t, res, NoData, []string{"www.example"}, 50)
}

func TestExampleEmptyNonTerminal(t *testing.T) {
	// Only c.example -> a.d.example is cached (no d.example record).
	// d.example is covered (wrap: d.example > c.example) and
	// k == labels(d.example) == 2, so it is an empty non-terminal.
	c := mustNew(t, "example", 200, 16)
	mustInsert(t, c, 1000, Record{
		Owner: "c.example", Next: "a.d.example",
		Types: []uint16{TypeNSEC}, TTL: 300, Validated: true,
	})
	res := mustLookup(t, c, 1100, "d.example", 1)
	checkResult(t, res, NoData, []string{"c.example"}, 100)
}

func TestExampleExpiryBoundary(t *testing.T) {
	c := exampleCache(t)
	// Both records expire exactly at 1200; alive requires now < expiry.
	res := mustLookup(t, c, 1200, "b.example", 1)
	checkResult(t, res, Miss, []string{}, 0)
	res = mustLookup(t, c, 1200, "d.example", 15)
	checkResult(t, res, Miss, []string{}, 0)
	// One second earlier everything is still alive.
	c2 := exampleCache(t)
	res = mustLookup(t, c2, 1199, "b.example", 1)
	checkResult(t, res, NXDomain, []string{"example"}, 1)
}

func TestWildcardNotCovered(t *testing.T) {
	// a.example -> c.example covers b.example, but *.example sorts below
	// a.example ('*' < 'a') and is covered by nothing: Miss.
	c := mustNew(t, "example", 200, 16)
	mustInsert(t, c, 100, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{TypeNSEC}, TTL: 300, Validated: true,
	})
	res := mustLookup(t, c, 150, "b.example", 1)
	checkResult(t, res, Miss, []string{}, 0)
}

func TestWildcardOwnerExists(t *testing.T) {
	// The wildcard name itself exists in the cache, so no NXDOMAIN proof.
	c := mustNew(t, "example", 200, 16)
	mustInsert(t, c, 100, Record{
		Owner: "example", Next: "x.example",
		Types: []uint16{TypeNSEC}, TTL: 300, Validated: true,
	})
	mustInsert(t, c, 100, Record{
		Owner: "*.example", Next: "example",
		Types: []uint16{1, TypeNSEC}, TTL: 300, Validated: true,
	})
	res := mustLookup(t, c, 150, "b.example", 1)
	checkResult(t, res, Miss, []string{}, 0)
}

func TestCanonicalOrder(t *testing.T) {
	must := func(s string) name {
		n, ok := parseName(s)
		if !ok {
			t.Fatalf("parseName(%q) failed", s)
		}
		return n
	}
	// a.example < z.a.example < b.example (right-side labels decide:
	// the descendant z.a.example sorts before b.example because a < b).
	if compareLabels(must("a.example").labels, must("z.a.example").labels) >= 0 {
		t.Fatal("a.example !< z.a.example")
	}
	if compareLabels(must("z.a.example").labels, must("b.example").labels) >= 0 {
		t.Fatal("z.a.example !< b.example")
	}
	// Ancestors sort before descendants.
	if compareLabels(must("example").labels, must("a.example").labels) >= 0 {
		t.Fatal("example !< a.example")
	}
	// Case-insensitive with optional trailing dot.
	if compareLabels(must("EXAMPLE.").labels, must("example").labels) != 0 {
		t.Fatal("EXAMPLE. != example")
	}
	// Shorter label that is a prefix sorts first; '*' < digits < letters.
	if compareLabels(must("*.example").labels, must("0.example").labels) >= 0 {
		t.Fatal("*.example !< 0.example")
	}
	if compareLabels(must("a.example").labels, must("ab.example").labels) >= 0 {
		t.Fatal("a.example !< ab.example")
	}
}

func TestParseNameValidation(t *testing.T) {
	good := []string{"example", "example.", "a-b_c*.example", "x.y.z"}
	for _, s := range good {
		if _, ok := parseName(s); !ok {
			t.Errorf("parseName(%q) = false, want true", s)
		}
	}
	bad := []string{
		"", ".", "a..b", "a b.example", "a+b.example", "éxample",
		fmt.Sprintf("%064s.example", "a"),
		fmt.Sprintf("%0254s", "a"),
	}
	for _, s := range bad {
		if _, ok := parseName(s); ok {
			t.Errorf("parseName(%q) = true, want false", s)
		}
	}
}

func TestReplaceSameOwner(t *testing.T) {
	c := mustNew(t, "example", 200, 2)
	mustInsert(t, c, 100, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{1, TypeNSEC}, TTL: 100, Validated: true,
	})
	// Replacement changes the type bitmap and restarts the lifetime.
	mustInsert(t, c, 150, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{TypeNSEC}, TTL: 200, Validated: true,
	})
	res := mustLookup(t, c, 200, "a.example", 1)
	checkResult(t, res, NoData, []string{"a.example"}, 150)
	if got := len(c.sorted); got != 1 {
		t.Fatalf("stored records = %d, want 1", got)
	}
}

func TestZeroEffectiveTTLClearsOld(t *testing.T) {
	c := mustNew(t, "example", 200, 2)
	mustInsert(t, c, 100, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{1, TypeNSEC}, TTL: 100, Validated: true,
	})
	// TTL=0 -> eff=0: the old record is removed and nothing is stored.
	mustInsert(t, c, 150, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{1, TypeNSEC}, TTL: 0, Validated: true,
	})
	if got := len(c.sorted); got != 0 {
		t.Fatalf("stored records = %d, want 0", got)
	}
	res := mustLookup(t, c, 160, "a.example", 1)
	checkResult(t, res, Miss, []string{}, 0)

	// soaMin=0 makes every effective TTL zero.
	c2 := mustNew(t, "example", 0, 2)
	mustInsert(t, c2, 100, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{TypeNSEC}, TTL: 100, Validated: true,
	})
	if got := len(c2.sorted); got != 0 {
		t.Fatalf("stored records = %d, want 0", got)
	}
}

func TestEvictionOrder(t *testing.T) {
	c := mustNew(t, "example", 1000, 2)
	mustInsert(t, c, 100, Record{
		Owner: "b.example", Next: "c.example",
		Types: []uint16{TypeNSEC}, TTL: 50, Validated: true, // expires 150
	})
	mustInsert(t, c, 100, Record{
		Owner: "d.example", Next: "e.example",
		Types: []uint16{TypeNSEC}, TTL: 80, Validated: true, // expires 180
	})
	// Full: inserting a new owner evicts the smallest expiry (b.example).
	mustInsert(t, c, 100, Record{
		Owner: "f.example", Next: "g.example",
		Types: []uint16{TypeNSEC}, TTL: 90, Validated: true,
	})
	if _, ok := c.entries["b.example"]; ok {
		t.Fatal("b.example should have been evicted")
	}
	if len(c.sorted) != 2 {
		t.Fatalf("stored records = %d, want 2", len(c.sorted))
	}

	// Ties on expiry are broken by the canonically smaller owner.
	c2 := mustNew(t, "example", 1000, 2)
	for _, owner := range []string{"d.example", "b.example"} {
		mustInsert(t, c2, 100, Record{
			Owner: owner, Next: "zz.example",
			Types: []uint16{TypeNSEC}, TTL: 50, Validated: true,
		})
	}
	mustInsert(t, c2, 100, Record{
		Owner: "f.example", Next: "zz.example",
		Types: []uint16{TypeNSEC}, TTL: 60, Validated: true,
	})
	if _, ok := c2.entries["b.example"]; ok {
		t.Fatal("b.example (smaller owner, tied expiry) should be evicted")
	}
	if _, ok := c2.entries["d.example"]; !ok {
		t.Fatal("d.example should have survived")
	}

	// Replacing an existing owner never triggers eviction.
	c3 := mustNew(t, "example", 1000, 1)
	mustInsert(t, c3, 100, Record{
		Owner: "a.example", Next: "b.example",
		Types: []uint16{TypeNSEC}, TTL: 50, Validated: true,
	})
	mustInsert(t, c3, 100, Record{
		Owner: "a.example", Next: "c.example",
		Types: []uint16{TypeNSEC}, TTL: 60, Validated: true,
	})
	if len(c3.sorted) != 1 || c3.sorted[0].next.String() != "c.example" {
		t.Fatalf("replacement failed: %+v", c3.sorted)
	}
}

func TestErrorPriority(t *testing.T) {
	c := mustNew(t, "example", 200, 4)
	mustInsert(t, c, 1000, Record{
		Owner: "a.example", Next: "b.example",
		Types: []uint16{TypeNSEC}, TTL: 100, Validated: true,
	})
	bad := Record{
		Owner: "other.com", Next: "z.other.com",
		Types: []uint16{TypeNSEC}, TTL: 100, Validated: false,
	}
	// Invalid parameter beats clock regression, not-validated and zone.
	if err := c.Insert(999, Record{Owner: "bad..name", Next: "b.example",
		Types: []uint16{TypeNSEC}, TTL: 100, Validated: false}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("bad name: %v", err)
	}
	if err := c.Insert(999, Record{Owner: "x.example", Next: "y.example",
		Types: []uint16{1}, TTL: 100, Validated: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("missing NSEC type: %v", err)
	}
	if err := c.Insert(999, Record{Owner: "x.example", Next: "y.example",
		Types: []uint16{TypeNSEC}, TTL: 86401, Validated: true}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("TTL out of range: %v", err)
	}
	if err := c.Insert(maxNow+1, bad); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("now out of range: %v", err)
	}
	// Clock regression beats not-validated and out-of-zone.
	if err := c.Insert(999, bad); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("clock regression: %v", err)
	}
	// Not-validated beats out-of-zone.
	if err := c.Insert(1000, bad); !errors.Is(err, ErrNotValidated) {
		t.Fatalf("not validated: %v", err)
	}
	// Out-of-zone is reported last.
	ok := bad
	ok.Validated = true
	if err := c.Insert(1000, ok); !errors.Is(err, ErrOutOfZone) {
		t.Fatalf("out of zone: %v", err)
	}
	// Rejected operations change neither records nor the clock.
	if len(c.sorted) != 1 {
		t.Fatalf("stored records = %d, want 1", len(c.sorted))
	}
	if c.lastNow != 1000 {
		t.Fatalf("lastNow = %d, want 1000", c.lastNow)
	}
	// Lookup errors: qtype range and zone.
	if _, err := c.Lookup(1000, "a.example", 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("qtype 0: %v", err)
	}
	if _, err := c.Lookup(1000, "a.example", 65536); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("qtype 65536: %v", err)
	}
	if _, err := c.Lookup(999, "a.example", 1); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("lookup clock: %v", err)
	}
	if _, err := c.Lookup(1000, "other.com", 1); !errors.Is(err, ErrOutOfZone) {
		t.Fatalf("lookup zone: %v", err)
	}
	// The apex itself is in-zone; unrelated TLDs and parents are not.
	if _, err := c.Lookup(1000, "example", 1); err != nil {
		t.Fatalf("apex lookup: %v", err)
	}
	if _, err := c.Lookup(1000, "com", 1); !errors.Is(err, ErrOutOfZone) {
		t.Fatalf("parent lookup: %v", err)
	}
}

func TestConstructorValidation(t *testing.T) {
	if _, err := New("", 100, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty zone: %v", err)
	}
	if _, err := New("example", 86401, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("soaMin too large: %v", err)
	}
	if _, err := New("example", 100, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("capacity 0: %v", err)
	}
	if _, err := New("example", 100, 4097); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("capacity 4097: %v", err)
	}
	if _, err := New("example", 100, 4096); err != nil {
		t.Fatalf("capacity 4096: %v", err)
	}
}
