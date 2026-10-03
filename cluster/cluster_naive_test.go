package cluster

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type naiveTemplate struct {
	id    int64
	words []string
	count int64
}

type naiveTenant struct {
	nextID   int64
	overflow int64
	leaves   map[string][]*naiveTemplate
}

type naiveMerger struct {
	theta   int
	max     int64
	tenants map[string]*naiveTenant
}

func TestMatchesNaiveSimulation(t *testing.T) {
	real, _ := New(50, 20, 2)
	sim := naiveMerger{theta: 50, max: 20, tenants: map[string]*naiveTenant{}}
	words := []string{"a", "b", "c", "d"}
	for i := 0; i < 160; i++ {
		tenant := []string{"alice", "bob"}[i%2]
		msg := fmt.Sprintf("%s %s %s %s", words[(i/16)%4], words[(i/4)%4], words[i%4], words[(i*3)%4])
		got := ingest(t, real, tenant, msg)
		want := sim.ingest(tenant, strings.Split(msg, " "))
		t.Logf("naive input=%s output=%+v expected=%+v decision=same threshold, tie, mutation and overflow rule", msg, got, want)
		if got != want {
			t.Fatalf("i=%d got=%+v want=%+v", i, got, want)
		}
	}
	for tenant := range sim.tenants {
		infos, overflow, _ := real.Templates(tenant)
		wantTexts, wantOverflow := sim.snapshot(tenant)
		gotTexts := make([]string, len(infos))
		for i, info := range infos {
			gotTexts[i] = fmt.Sprintf("%s#%d", info.Text, info.Count)
		}
		if !reflect.DeepEqual(gotTexts, wantTexts) || overflow != wantOverflow {
			t.Fatalf("%s snapshots differ: %v/%d vs %v/%d", tenant, gotTexts, overflow, wantTexts, wantOverflow)
		}
	}
}

func (s *naiveMerger) tenant(name string) *naiveTenant {
	state := s.tenants[name]
	if state == nil {
		state = &naiveTenant{leaves: map[string][]*naiveTemplate{}}
		s.tenants[name] = state
	}
	return state
}

func (s *naiveMerger) ingest(tenant string, tok []string) Result {
	state := s.tenant(tenant)
	key := fmt.Sprintf("%d:%s", len(tok), tok[0])
	best, bestEq, bestWild := -1, -1, 0
	for i, candidate := range state.leaves[key] {
		eq, wild := 0, 0
		for p, word := range candidate.words {
			if word == "<*>" {
				eq, wild = eq+1, wild+1
			} else if word == tok[p] {
				eq++
			}
		}
		if eq*100 >= len(tok)*s.theta && (best == -1 || eq > bestEq || (eq == bestEq && wild < bestWild) || (eq == bestEq && wild == bestWild && candidate.id < state.leaves[key][best].id)) {
			best, bestEq, bestWild = i, eq, wild
		}
	}
	if best >= 0 {
		chosen := state.leaves[key][best]
		for p, word := range chosen.words {
			if word != tok[p] && word != "<*>" {
				chosen.words[p] = "<*>"
			}
		}
		chosen.count++
		return Result{ID: chosen.id}
	}
	if state.nextID >= s.max {
		state.overflow++
		return Result{Overflow: true}
	}
	state.nextID++
	state.leaves[key] = append(state.leaves[key], &naiveTemplate{id: state.nextID, words: append([]string(nil), tok...), count: 1})
	return Result{ID: state.nextID, Created: true}
}

func (s *naiveMerger) snapshot(tenant string) ([]string, int64) {
	state := s.tenants[tenant]
	byID := map[int64]*naiveTemplate{}
	for _, list := range state.leaves {
		for _, item := range list {
			byID[item.id] = item
		}
	}
	out := []string{}
	for id := int64(1); id <= state.nextID; id++ {
		if item := byID[id]; item != nil {
			out = append(out, fmt.Sprintf("%s#%d", strings.Join(item.words, " "), item.count))
		}
	}
	return out, state.overflow
}

func TestConcurrentIngestAndSetTheta(t *testing.T) {
	m, _ := New(1, 100000, 2)
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				tenant := []string{"alice", "bob"}[(worker+i)%2]
				msg := fmt.Sprintf("word%d pos%d tail%d", i%17, i%11, i%5)
				result, err := m.Ingest(tenant, msg)
				if err != nil {
					t.Errorf("Ingest(%q, %q): %v", tenant, msg, err)
					return
				}
				t.Logf("concurrent input tenant=%q msg=%q output=%+v decision=atomic tenant cluster operation", tenant, msg, result)
			}
		}(worker)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for theta := 1; theta <= 100; theta++ {
			if err := m.SetTheta(theta); err != nil {
				t.Errorf("SetTheta(%d): %v", theta, err)
				return
			}
			t.Logf("concurrent input=SetTheta output=%d decision=advance one generation only", theta)
		}
	}()

	wg.Wait()

	for _, tenant := range []string{"alice", "bob"} {
		infos, overflow, err := m.Templates(tenant)
		if err != nil {
			t.Fatal(err)
		}
		var count int64
		lastID := int64(0)
		for _, info := range infos {
			if info.ID <= lastID || info.ID > 100000 {
				t.Fatalf("bad id ordering: %+v", infos)
			}
			lastID = info.ID
			count += info.Count
		}
		if count+overflow != 1000 || len(infos) > 100000 {
			t.Fatalf("tenant %s count=%d overflow=%d templates=%d", tenant, count, overflow, len(infos))
		}
	}

	if m.Gen() != 100 {
		t.Fatalf("Gen=%d, want 100", m.Gen())
	}
}
