package ontology

import (
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

type naiveState struct {
	c   int
	e   int
	u   int64
	win []bool
}

type naiveConfig struct {
	n  int
	k  int
	b  int64
	cp int64
	p  int
	wn int
	q  int
}

type ejectorSnapshot struct {
	maxNow int64
	hosts  []hostState
}

func snapshotEjector(ejector *OutlierEjector) ejectorSnapshot {
	hosts := make([]hostState, len(ejector.hosts))
	for i := range ejector.hosts {
		hosts[i] = ejector.hosts[i]
		hosts[i].window = append([]bool(nil), ejector.hosts[i].window...)
	}
	return ejectorSnapshot{maxNow: ejector.maxNow, hosts: hosts}
}

func naiveReport(cfg naiveConfig, states []naiveState, host int, ok bool, now int64) (Result, string) {
	state := &states[host]
	if state.u > now {
		return ResultIgnored, fmt.Sprintf("u=%d>now=%d", state.u, now)
	}

	appendNaiveWindow(cfg.wn, state, ok)
	if ok {
		state.c = 0
		if state.e > 0 {
			state.e--
		}
		return ResultRecorded, "success reset c and decremented e"
	}

	state.c++
	failures := 0
	for _, success := range state.win {
		if !success {
			failures++
		}
	}

	consecutive := state.c >= cfg.k
	fullWindow := len(state.win) == cfg.wn
	windowRate := fullWindow && failures*100 >= cfg.q*cfg.wn
	if !consecutive && !windowRate {
		return ResultRecorded, fmt.Sprintf("c=%d<K=%d and window=%d/%d failures=%d", state.c, cfg.k, len(state.win), cfg.wn, failures)
	}

	ejected := 0
	for candidate := range states {
		if states[candidate].u > now {
			ejected++
		}
	}

	trigger := describeTrigger(consecutive, windowRate)
	allowedLimit := new(big.Int).Mul(big.NewInt(int64(cfg.p)), big.NewInt(int64(cfg.n)))
	requested := new(big.Int).Mul(big.NewInt(int64(ejected+1)), big.NewInt(100))
	if requested.Cmp(allowedLimit) > 0 {
		return ResultBlocked, fmt.Sprintf("%s; %d*100>P*N=%d", trigger, (ejected+1)*100, cfg.p*cfg.n)
	}

	state.e++
	duration := cfg.b * int64(state.e)
	if duration > cfg.cp {
		duration = cfg.cp
	}
	state.u = now + duration
	state.c = 0
	state.win = nil
	return ResultEjected, fmt.Sprintf("%s; %d*100<=%d; duration=%d; u=%d", trigger, (ejected+1)*100, cfg.p*cfg.n, duration, state.u)
}

func appendNaiveWindow(size int, state *naiveState, ok bool) {
	state.win = append(state.win, ok)
	if len(state.win) > size {
		state.win = append([]bool(nil), state.win[1:]...)
	}
}

func describeTrigger(consecutive, windowRate bool) string {
	switch {
	case consecutive && windowRate:
		return "consecutive and window-rate triggers"
	case consecutive:
		return "consecutive trigger"
	default:
		return "window-rate trigger"
	}
}

func normalizedWindow(win []bool) []bool {
	if len(win) == 0 {
		return nil
	}
	return append([]bool(nil), win...)
}

func assertNaiveState(t *testing.T, ejector *OutlierEjector, states []naiveState, maxNow int64) {
	t.Helper()
	for host, want := range states {
		got := ejector.hosts[host]
		if got.consecutiveFailures != want.c || got.ejectionCount != want.e || got.ejectionEnd != want.u ||
			!reflect.DeepEqual(normalizedWindow(got.window), normalizedWindow(want.win)) {
			t.Fatalf("host %d state = (c=%d,e=%d,u=%d,win=%v), want (c=%d,e=%d,u=%d,win=%v)",
				host, got.consecutiveFailures, got.ejectionCount, got.ejectionEnd, got.window,
				want.c, want.e, want.u, want.win)
		}
	}
	if ejector.maxNow != maxNow {
		t.Fatalf("maxNow = %d, want %d", ejector.maxNow, maxNow)
	}
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for iteration := 0; iteration < 2000; iteration++ {
		cfg := naiveConfig{
			n:  rng.Intn(8) + 1,
			k:  rng.Intn(5) + 1,
			b:  int64(rng.Intn(20) + 1),
			cp: int64(rng.Intn(30) + 20),
			p:  rng.Intn(101),
			wn: rng.Intn(8) + 1,
			q:  rng.Intn(100) + 1,
		}
		if cfg.cp < cfg.b {
			cfg.cp = cfg.b
		}

		ejector, err := NewOutlierEjector(cfg.n, cfg.k, cfg.b, cfg.cp, cfg.p, cfg.wn, cfg.q)
		if err != nil {
			t.Fatalf("iteration %d: NewOutlierEjector() error = %v", iteration, err)
		}
		states := make([]naiveState, cfg.n)
		current := int64(0)

		for op := 0; op < rng.Intn(25)+1; op++ {
			events := make([]Event, rng.Intn(5)+1)
			batchMax := current
			for i := range events {
				now := current + int64(rng.Intn(4))
				if now > batchMax {
					batchMax = now
				}
				events[i] = Event{Host: rng.Intn(cfg.n), OK: rng.Intn(10) < 3, Now: now}
			}

			inputOrder := make([]Event, len(events))
			copy(inputOrder, events)
			rng.Shuffle(len(events), func(i, j int) {
				events[i], events[j] = events[j], events[i]
			})

			gotResults, gotErr := ejector.ReportBatch(events)
			if gotErr != nil {
				t.Fatalf("iteration %d op %d: ReportBatch() error = %v", iteration, op, gotErr)
			}

			sortedOrder := make([]int, len(events))
			for i := range sortedOrder {
				sortedOrder[i] = i
			}
			sort.SliceStable(sortedOrder, func(i, j int) bool {
				return events[sortedOrder[i]].Now < events[sortedOrder[j]].Now
			})

			wantResults := make([]Result, len(events))
			reasons := make([]string, len(events))
			for _, sortedIndex := range sortedOrder {
				event := events[sortedIndex]
				wantResults[sortedIndex], reasons[sortedIndex] = naiveReport(cfg, states, event.Host, event.OK, event.Now)
			}
			current = batchMax

			t.Logf("iteration=%d cfg={n:%d k:%d b:%d cap:%d p:%d wn:%d q:%d} input=%v shuffled=%v results=%v reasons=%v states=%v maxNow=%d",
				iteration, cfg.n, cfg.k, cfg.b, cfg.cp, cfg.p, cfg.wn, cfg.q, inputOrder, events, gotResults, reasons, states, current)

			if !reflect.DeepEqual(gotResults, wantResults) {
				t.Fatalf("iteration %d op %d results = %v, want %v", iteration, op, gotResults, wantResults)
			}
			assertNaiveState(t, ejector, states, current)

			for host := 0; host < cfg.n; host++ {
				gotEjected, err := ejector.Ejected(host, current)
				if err != nil {
					t.Fatalf("iteration %d Ejected(%d, %d) error = %v", iteration, host, current, err)
				}
				if wantEjected := states[host].u > current; gotEjected != wantEjected {
					t.Fatalf("iteration %d Ejected(%d, %d) = %v, want %v", iteration, host, current, gotEjected, wantEjected)
				}
			}

			wantHealthy := make([]int, 0, cfg.n)
			for host, state := range states {
				if state.u <= current {
					wantHealthy = append(wantHealthy, host)
				}
			}
			gotHealthy, err := ejector.Healthy(current)
			if err != nil || !reflect.DeepEqual(gotHealthy, wantHealthy) {
				t.Fatalf("iteration %d Healthy(%d) = (%v, %v), want %v", iteration, current, gotHealthy, err, wantHealthy)
			}
		}
	}
}

func TestConcurrentReportsAndQueries(t *testing.T) {
	ejector := newTestEjector(t, 32, 1, 10, 10, 100, 1, 1)
	var wait sync.WaitGroup
	for host := 0; host < 32; host++ {
		wait.Add(1)
		go func(host int) {
			defer wait.Done()
			result, err := ejector.Report(host, false, 1)
			if err != nil || result != ResultEjected {
				t.Errorf("Report(%d) = (%q, %v), want ejected", host, result, err)
			}
		}(host)
	}
	wait.Wait()

	for host := 0; host < 32; host++ {
		wait.Add(1)
		go func(host int) {
			defer wait.Done()
			ejected, err := ejector.Ejected(host, 2)
			if err != nil || !ejected {
				t.Errorf("Ejected(%d, 2) = (%v, %v), want true", host, ejected, err)
			}
			if _, err := ejector.Healthy(2); err != nil {
				t.Errorf("Healthy(2) error = %v", err)
			}
		}(host)
	}
	wait.Wait()
}

func TestReplayAndBatchPermutationDeterminism(t *testing.T) {
	sequences := [][]Event{
		{{0, false, 1}, {0, false, 2}, {0, false, 3}},
		{{1, false, 4}, {1, true, 5}, {1, false, 6}, {1, false, 7}},
		{{2, true, 8}, {2, false, 9}, {2, true, 10}, {2, false, 11}},
		{{3, false, 12}, {3, false, 13}, {3, false, 14}, {3, false, 15}, {3, false, 17}},
		{{0, false, 18}, {0, false, 19}, {0, false, 20}},
	}

	run := func() ([]Result, ejectorSnapshot) {
		ejector := newTestEjector(t, 4, 3, 10, 25, 50, 4, 50)
		var allResults []Result
		for _, sequence := range sequences {
			results, err := ejector.ReportBatch(sequence)
			if err != nil {
				t.Fatalf("ReportBatch(%v) error = %v", sequence, err)
			}
			allResults = append(allResults, results...)
		}
		return allResults, snapshotEjector(ejector)
	}

	firstResults, firstSnapshot := run()
	secondResults, secondSnapshot := run()
	if !reflect.DeepEqual(firstResults, secondResults) || !reflect.DeepEqual(firstSnapshot, secondSnapshot) {
		t.Fatalf("replay differs: results %v vs %v, snapshot %+v vs %+v",
			firstResults, secondResults, firstSnapshot, secondSnapshot)
	}

	first := []Event{
		{0, false, 2},
		{0, true, 2},
		{1, false, 1},
	}
	permuted := []Event{
		{1, false, 1},
		{0, false, 2},
		{0, true, 2},
	}

	ejectorA := newTestEjector(t, 2, 100, 10, 10, 100, 4, 100)
	resultsA, err := ejectorA.ReportBatch(first)
	if err != nil {
		t.Fatalf("ReportBatch(first) error = %v", err)
	}
	ejectorB := newTestEjector(t, 2, 100, 10, 10, 100, 4, 100)
	resultsB, err := ejectorB.ReportBatch(permuted)
	if err != nil {
		t.Fatalf("ReportBatch(permuted) error = %v", err)
	}
	if !reflect.DeepEqual(resultsA, resultsB) || !reflect.DeepEqual(snapshotEjector(ejectorA), snapshotEjector(ejectorB)) {
		t.Fatalf("same timestamp-relative order differs: results %v vs %v", resultsA, resultsB)
	}
}
