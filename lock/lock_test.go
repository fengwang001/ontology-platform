package lock

import (
	"fmt"
	"testing"
)

// naive is a scan-based reference: system ceiling computed by scanning
// all currently held resources on every Acquire.
type naive struct {
	prio   map[string]int
	ceil   map[string]int
	holder map[string]string
}

func (n naive) sys(task string) int {
	s := 0
	for r, h := range n.holder {
		if h != task {
			s = max(s, n.ceil[r])
		}
	}
	return s
}

// Invariant 1: every Acquire result and every system ceiling must equal
// the naive scan-based reference, over random operation sequences.
func TestNaiveConsistency(t *testing.T) {
	for _, trial := range []int{1, 7, 99} {
		m := New()
		nv := naive{prio: map[string]int{}, ceil: map[string]int{}, holder: map[string]string{}}
		for j := 0; j < 4; j++ {
			m.AddResource(fmt.Sprintf("r%d", j))
			nv.ceil[fmt.Sprintf("r%d", j)] = 5
		}
		for i := 0; i < 5; i++ {
			tk := fmt.Sprintf("t%d", i)
			m.AddTask(tk, i+1)
			nv.prio[tk] = i + 1
			for j := 0; j < 4; j++ {
				if err := m.Use(tk, fmt.Sprintf("r%d", j)); err != nil {
					t.Fatal(err)
				}
			}
		}
		seed := uint32(trial)
		for k := 0; k < 2000; k++ {
			seed = seed*1664525 + 1013904223
			tk := fmt.Sprintf("t%d", seed%5)
			r := fmt.Sprintf("r%d", (seed>>8)%4)
			switch {
			case nv.holder[r] == tk:
				if err := m.Release(tk, r); err != nil {
					t.Fatalf("trial %d step %d: %v", trial, k, err)
				}
				delete(nv.holder, r)
			case nv.holder[r] != "":
				if got, err := m.Acquire(tk, r); got || err != nil {
					t.Fatalf("trial %d step %d: held resource got %v,%v", trial, k, got, err)
				}
			default:
				got, err := m.Acquire(tk, r)
				if want := nv.prio[tk] > nv.sys(tk); got != want || err != nil {
					t.Fatalf("trial %d step %d: got %v,%v want %v", trial, k, got, err, want)
				}
				if got {
					nv.holder[r] = tk
				}
			}
			if c := m.SystemCeiling(); c != nv.sys("") {
				t.Fatalf("trial %d step %d: ceiling %d want %d", trial, k, c, nv.sys(""))
			}
		}
	}
}

// Complexity: with m resources held, one Acquire decision must examine
// only a small constant number of held resources (incremental
// maintenance), never proportional to m.
func TestAcquireCheckCount(t *testing.T) {
	for _, size := range []int{100, 1000, 10000} {
		m := New()
		m.AddTask("H", 1000000)
		m.AddTask("X", 10)
		m.AddResource("rx")
		if err := m.Use("X", "rx"); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < size; i++ {
			r := fmt.Sprintf("r%d", i)
			m.AddResource(r)
			if err := m.Use("H", r); err != nil {
				t.Fatal(err)
			}
			if ok, err := m.Acquire("H", r); !ok || err != nil {
				t.Fatalf("size %d: hold failed: %v %v", size, ok, err)
			}
		}
		got, err := m.Acquire("X", "rx") // blocked: 10 <= 1000000
		if err != nil || got {
			t.Fatalf("size %d: got %v,%v want blocked", size, got, err)
		}
		if m.checked > 2 {
			t.Fatalf("size %d: examined %d held resources, grows with m", size, m.checked)
		}
	}
}
