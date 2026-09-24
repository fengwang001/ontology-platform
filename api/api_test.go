package api

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func ck(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

// build replays write ops on a fresh cap=4 store and returns it.
func build(t *testing.T, ops [][3]string) *Store {
	s, err := New(4, 32, 64)
	ck(t, err)
	for _, o := range ops {
		ck(t, s.Write(o[1], o[2]))
	}
	return s
}
func probe(t *testing.T, ops [][3]string, k string) int {
	s := build(t, ops)
	b := s.DiskReads()
	s.Read(k)
	return s.DiskReads() - b
}
func TestReplayConsistency(t *testing.T) {
	idx := []int{0, 1, 0, 2, 1, 2, 0, 3, 2}
	var ops [][3]string
	latest := map[string]string{}
	for i, x := range idx {
		k, v := fmt.Sprintf("k%d", x), fmt.Sprintf("v%d-%d", x, i)
		latest[k] = v
		ops = append(ops, [3]string{"w", k, v})
	}
	s := build(t, ops)
	for k, w := range latest {
		if g, ok := s.Read(k); !ok || g != w {
			t.Fatalf("Read(%s)=%q,%v want %q", k, g, ok, w)
		}
	}
	if _, ok := s.Read("k9"); ok {
		t.Fatal("never-written k9 reported found")
	}
}
func TestWriteThroughColdOverwrite(t *testing.T) {
	s, _ := New(1, 32, 8)
	w := func(k, v string) { ck(t, s.Write(k, v)) }
	w("A", "1")
	w("B", "2")  // A evicted
	w("A", "10") // write-through cold A; B evicted
	w("B", "20")
	if v, ok := s.Read("A"); !ok || v != "10" || s.DiskReads() != 1 {
		t.Fatalf("A=%q,%v reads=%d want 10/1", v, ok, s.DiskReads())
	}
	if v, ok := s.Read("B"); !ok || v != "20" {
		t.Fatalf("B=%q,%v want 20", v, ok)
	}
}
func TestLRUPrecision(t *testing.T) {
	access := []string{"a", "b", "c", "d", "a", "e", "b", "f", "c"}
	var ops [][3]string
	for i, k := range access {
		ops = append(ops, [3]string{"w", k, fmt.Sprintf("v%d", i)})
	}
	seen, hot := map[string]bool{}, map[string]bool{}
	for i := len(access) - 1; i >= 0 && len(hot) < 4; i-- {
		if !seen[access[i]] {
			seen[access[i]], hot[access[i]] = true, true
		}
	}
	for _, k := range access {
		if d := probe(t, ops, k); (d == 0) != hot[k] {
			t.Fatalf("%s hot=%v delta=%d", k, hot[k], d)
		}
	}
}
func TestRejectedLeavesNoTrace(t *testing.T) {
	if _, e := New(0, 4, 4); !errors.Is(e, ErrInvalidCapacity) {
		t.Fatal("memCap<=0 not rejected with ErrInvalidCapacity")
	}
	s, _ := New(2, 4, 3)
	ck(t, s.Write("a", "1"))
	for _, c := range []struct {
		k, v string
		want error
	}{
		{"", "v", ErrEmptyKey}, {"a", "", ErrEmptyValue}, {"longkey", "v", ErrKeyTooLong},
	} {
		if e := s.Write(c.k, c.v); !errors.Is(e, c.want) {
			t.Fatalf("Write(%q)=%v want %v", c.k, e, c.want)
		}
	}
	ck(t, s.Write("b", "2"))
	ck(t, s.Write("c", "3"))
	if e := s.Write("d", "4"); !errors.Is(e, ErrTooManyKeys) {
		t.Fatalf("4th distinct key err=%v want ErrTooManyKeys", e)
	}
	if v, ok := s.Read("d"); ok {
		t.Fatalf("rejected key d persisted as %q", v)
	}
	if v, ok := s.Read("a"); !ok || v != "1" {
		t.Fatalf("state changed after rejects: a=%q,%v", v, ok)
	}
}
func TestConcurrentReaders(t *testing.T) {
	const G, K = 8, 8
	s, _ := New(K, 32, 32)
	for i := 0; i < K; i++ {
		ck(t, s.Write(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i)))
	}
	var start, done sync.WaitGroup
	start.Add(1)
	res := make([]map[string]string, G)
	dr := make([]int, G)
	for g := 0; g < G; g++ {
		done.Add(1)
		go func(g int) {
			defer done.Done()
			start.Wait()
			m := map[string]string{}
			for i := 0; i < K; i++ {
				v, ok := s.Read(fmt.Sprintf("k%d", i))
				if !ok {
					t.Errorf("missing key %d", i)
				}
				m[fmt.Sprintf("k%d", i)] = v
			}
			dr[g], res[g] = s.DiskReads(), m
		}(g)
	}
	start.Done()
	done.Wait()
	for g := 1; g < G; g++ {
		if fmt.Sprint(res[g]) != fmt.Sprint(res[0]) || dr[g] != dr[0] {
			t.Fatalf("goroutine %d disagrees with goroutine 0", g)
		}
	}
}
func TestSelfCheck(t *testing.T) {
	s, e := New(2, 32, 32)
	ck(t, e)
	ck(t, s.SelfCheck())
}
