package store

import (
	"fmt"
	"maps"
	"math/rand"
	"reflect"
	"slices"
	"sort"
	"sync"
	"testing"
)

// model replays the same calls and tracks each key's last-access step so
// the expected LRU hot set is derived without peering at internals.
type model struct {
	vals map[string]string
	at   map[string]int64
	step int64
}

func newModel() *model             { return &model{vals: map[string]string{}, at: map[string]int64{}} }
func (m *model) write(k, v string) { m.step++; m.vals[k], m.at[k] = v, m.step }
func (m *model) read(k string) bool {
	if _, ok := m.vals[k]; !ok {
		return false
	}
	m.step++
	m.at[k] = m.step
	return true
}
func (m *model) expectHot(c int) []string {
	ks := append([]string{}, slices.Collect(maps.Keys(m.at))...)
	sort.Slice(ks, func(i, j int) bool {
		a, b := ks[i], ks[j]
		return m.at[a] > m.at[b] || m.at[a] == m.at[b] && a < b // MRU first; ties by key
	})
	if len(ks) > c {
		ks = ks[:c]
	}
	sort.Strings(ks)
	return ks
}

// Op strings encode the mandated trace: first char w/r, then the key,
// then for writes the value and for reads the expected value.
func TestSevenStepTable(t *testing.T) {
	s, _ := NewStore(2)
	ops := []string{"wA1", "wB2", "rA1", "wC3", "rB2", "wB20", "rB20"}
	hot := [][]string{{"A"}, {"A", "B"}, {"A", "B"}, {"A", "C"}, {"B", "C"}, {"B", "C"}, {"B", "C"}}
	drs := []int{0, 0, 0, 0, 1, 1, 1}
	for i, op := range ops {
		if op[0] == 'w' {
			s.Write(op[1:2], op[2:])
		} else if v, ok := s.Read(op[1:2]); v != op[2:] || !ok {
			t.Fatalf("step%d Read(%s)=%q,%v want %q", i+1, op[1:2], v, ok, op[2:])
		}
		if got := s.HotKeys(); !reflect.DeepEqual(got, hot[i]) {
			t.Fatalf("step%d hot=%v want %v (step4 must evict B)", i+1, got, hot[i])
		}
		if s.DiskReads() != drs[i] {
			t.Fatalf("step%d diskReads=%d want %d", i+1, s.DiskReads(), drs[i])
		}
	}
	if v, ok := s.Read("D"); ok || v != "" { // never written -> absence, not zero value
		t.Fatalf("Read(D)=%q,%v want \"\",false", v, ok)
	}
}

func TestReplayAndWriteThrough(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 40; iter++ {
		c := 1 + rng.Intn(8)
		s, err := NewStore(c)
		if err != nil {
			t.Fatal(err)
		}
		m := newModel()
		for i := 0; i < 100; i++ {
			k := string(rune('A' + rng.Intn(6)))
			if rng.Intn(2) == 0 {
				v := string(rune('1' + rng.Intn(5)))
				s.Write(k, v)
				m.write(k, v)
			} else {
				got, ok := s.Read(k)
				if wantOK := m.read(k); ok != wantOK || ok && got != m.vals[k] {
					t.Fatalf("Read(%q)=%q,%v want %q,%v", k, got, ok, m.vals[k], wantOK)
				}
			}
			if got := s.HotKeys(); !reflect.DeepEqual(got, m.expectHot(c)) {
				t.Fatalf("iter%d step%d hot=%v want %v", iter, i, got, m.expectHot(c))
			}
			for dk, dv := range m.vals { // disk never stale: write-through
				if s.disk[dk] != dv {
					t.Fatalf("disk[%q]=%q want %q", dk, s.disk[dk], dv)
				}
			}
		}
	}
}

func TestEvictionScannedIsConstant(t *testing.T) {
	for _, c := range []int{100, 1000, 10000} {
		s, _ := NewStore(c)
		for i := 0; i < c; i++ {
			s.Write(fmt.Sprintf("k%05d", i), "v")
		}
		s.Write("trigger", "v")
		if s.lastEvictScanned != 1 { // one heap root regardless of cap: O(1), not a scan
			t.Fatalf("cap=%d scanned=%d, want 1", c, s.lastEvictScanned)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	const c, n = 64, 16
	s, _ := NewStore(c)
	for i := 0; i < c; i++ {
		s.Write(fmt.Sprintf("k%02d", i), fmt.Sprintf("v%d", i))
	}
	start, wg, reads := make(chan struct{}), sync.WaitGroup{}, make([]int, n)
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			for r := 0; r < 200; r++ {
				for i := 0; i < c; i++ {
					k, wv := fmt.Sprintf("k%02d", i), fmt.Sprintf("v%d", i)
					if v, ok := s.Read(k); !ok || v != wv {
						t.Errorf("Read(%q)=%q,%v want %q", k, v, ok, wv)
					}
				}
			}
			reads[g] = s.DiskReads() // every goroutine must see the same count
		}(g)
	}
	close(start)
	wg.Wait()
	for g := 1; g < n; g++ {
		if reads[g] != reads[0] {
			t.Fatalf("disk reads disagree: %d vs %d", reads[g], reads[0])
		}
	}
}
