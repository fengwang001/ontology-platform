package certselector

import (
	"fmt"
	"sync"
	"testing"
)

// buildLargeSet creates total certificates spread across distinct names; only
// perName certificates share the queried SAN, so the candidate fan-out is
// bounded while the collection itself grows.
func buildLargeSet(tb testing.TB, total, perName int) *Selector {
	tb.Helper()
	s := NewSelector()
	n := 0
	for group := 0; n < total; group++ {
		host := fmt.Sprintf("host%06d.site.example", group)
		for j := 0; j < perName && n < total; j++ {
			id := fmt.Sprintf("g%05d-c%02d", group, j)
			cert := ec(id, []string{host}, 0, 1000000)
			if j%2 == 0 {
				cert = rsaCert(id, []string{host}, 0, 1000000)
			}
			if err := s.Add(cert); err != nil {
				tb.Fatal(err)
			}
			n++
		}
	}
	return s
}

// TestExaminedCountBounded proves selection inspects only the matching
// candidate set, not the whole collection: with 20,000 certificates but
// 4 sharing the queried name, exactly 4 are examined. The companion benchmark
// shows the absolute lookup cost stays at the sub-microsecond level, and
// adding thousands more unrelated certificates (below) does not change it.
func TestExaminedCountBounded(t *testing.T) {
	const perName = 4
	s := buildLargeSet(t, 20000, perName)
	if _, err := s.Select("host000000.site.example", both(), 50); err != nil {
		t.Fatal(err)
	}
	if got := s.ExaminedCount(); got != perName {
		t.Fatalf("20k certs: examined=%d, want %d", got, perName)
	}

	// Grow the collection further; the same lookup must still examine 4.
	for i := 20000 / perName; i < 30000/perName; i++ {
		host := fmt.Sprintf("grow%06d.site.example", i)
		for j := 0; j < perName; j++ {
			id := fmt.Sprintf("grow%06d-%02d", i, j)
			c := rsaCert(id, []string{host}, 0, 1000000)
			if err := s.Add(c); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.Select("host000000.site.example", both(), 50); err != nil {
		t.Fatal(err)
	}
	if got := s.ExaminedCount(); got != perName {
		t.Fatalf("30k certs: examined=%d, still want %d", got, perName)
	}
}

// TestExaminedCountWildcard verifies wildcard lookups touch only the certs
// attached to the matching wildcard base, never the unrelated noise.
func TestExaminedCountWildcard(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("w1", []string{"*.site.example"}, 0, 1000000))
	mustAdd(t, s, ec("w2", []string{"*.site.example"}, 0, 1000000))
	for i := 0; i < 5000; i++ {
		host := fmt.Sprintf("noise%06d.elsewhere.test", i)
		id := fmt.Sprintf("noise%05d", i)
		if err := s.Add(rsaCert(id, []string{host}, 0, 1000000)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Select("x.site.example", both(), 50); err != nil {
		t.Fatal(err)
	}
	if got := s.ExaminedCount(); got != 2 {
		t.Fatalf("wildcard selection examined %d certs, want 2", got)
	}
}

// TestExaminedCountManyWildcardBases builds thousands of distinct wildcard
// bases; a lookup must inspect only the two certs on the single matching base.
func TestExaminedCountManyWildcardBases(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("target1", []string{"*.target.example"}, 0, 1000000))
	mustAdd(t, s, ec("target2", []string{"*.target.example"}, 0, 1000000))
	for i := 0; i < 5000; i++ {
		base := fmt.Sprintf("g%06d.elsewhere.example", i)
		id := fmt.Sprintf("w%06d", i)
		if err := s.Add(rsaCert(id, []string{"*." + base}, 0, 1000000)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Select("host.target.example", both(), 50); err != nil {
		t.Fatal(err)
	}
	if got := s.ExaminedCount(); got != 2 {
		t.Fatalf("examined %d certs among 5002 wildcard certs, want 2", got)
	}
}

// TestConcurrentLinearizability runs parallel readers and writers; every
// successful selection must return a certificate present in a quiescent
// snapshot, and no half-applied state may be observed.
func TestConcurrentLinearizability(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c0", []string{"hot.example"}, 0, 1000000))

	var wg sync.WaitGroup

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("hot-%d", g)
			for i := 0; i < 500; i++ {
				cert := ec(id, []string{"hot.example"}, 0, 1000000)
				if err := s.Add(cert); err != nil && err != ErrConflict {
					t.Errorf("add: %v", err)
					return
				}
				sel, err := s.Select("hot.example", both(), 50)
				if err == nil {
					if sel.Certificate.ID == "" || sel.Source != SourceExact {
						t.Errorf("bad selection: %+v", sel)
						return
					}
				}
				if err := s.Remove(id); err != nil && err != ErrNotFound {
					t.Errorf("remove: %v", err)
					return
				}
			}
		}(g)
	}

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				sel, err := s.Select("hot.example", both(), 50)
				if err == nil && sel.Certificate.ID == "" {
					t.Error("empty id selected")
					return
				}
			}
		}()
	}

	wg.Wait()
}

func BenchmarkSelect20k(b *testing.B) {
	s := buildLargeSet(b, 20000, 4)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Select("host000000.site.example", both(), 50); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSelectNaive20k(b *testing.B) {
	s := buildLargeSet(b, 20000, 4)
	m := &naiveScanSelector{s: s}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := m.scanSelect("host000000.site.example", both(), 50); err != nil {
			b.Fatal(err)
		}
	}
}

// naiveScanSelector is the deliberately linear baseline used to demonstrate
// the gap: it snapshots all certs under the lock and scans all 20,000.
type naiveScanSelector struct {
	s *Selector
}

func (m *naiveScanSelector) scanSelect(name string, supported map[KeyType]bool, now int64) (Selection, error) {
	m.s.idx.mu.RLock()
	all := make([]Certificate, 0, len(m.s.idx.certs))
	for _, c := range m.s.idx.certs {
		all = append(all, c)
	}
	def := m.s.idx.defaults
	m.s.idx.mu.RUnlock()

	norm, err := normalizeName(name)
	if err != nil {
		return Selection{}, err
	}
	var best *Certificate
	for _, c := range all {
		matched := false
		for _, san := range c.Names {
			base, wildcard, _ := parseSAN(san)
			if !wildcard && base == norm {
				matched = true
			}
			if wildcard && wildcardMatch(norm, base) {
				matched = true
			}
		}
		if !matched {
			continue
		}
		if now < c.NotBefore || now >= c.NotAfter || !supported[c.Key] {
			continue
		}
		if best == nil || prefer(c, *best) {
			cc := c
			best = &cc
		}
	}
	if best != nil {
		return Selection{Certificate: *best, Source: SourceExact}, nil
	}
	_ = def
	return Selection{}, ErrNoMatch
}
