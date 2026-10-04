package quarantine

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/history"
)

func mustPlanner(t *testing.T, w, f, p, r, d int) *Planner {
	t.Helper()
	planner, err := New(w, f, p, r, d)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return planner
}

func report(t *testing.T, planner *Planner, build, test string, pass bool, ms int) int {
	t.Helper()
	err := planner.Report(build, test, pass, ms)
	if err != nil {
		t.Fatalf("Report(%q, %q, %v, %d) error = %v", build, test, pass, ms, err)
	}
	return planner.touchedCount()
}

func TestPlanSnapshotAndLifecycleExample(t *testing.T) {
	planner := mustPlanner(t, 5, 2, 2, 1, 3)

	for index, mark := range []history.Mark{history.Flaky, history.Clean, history.Flaky} {
		build := fmt.Sprintf("build-%d", index+1)
		plan, err := planner.Plan(build, []string{"x"}, 1)
		if err != nil {
			t.Fatalf("Plan(%s) error = %v", build, err)
		}
		t.Logf("input=%s output=%v reason snapshot quarantine=%v", build, plan, index >= 3)
		if len(plan) != 1 || !reflect.DeepEqual(plan[0], []string{"x"}) {
			t.Fatalf("plan = %v, want x in regular shard", plan)
		}
		if mark == history.Flaky {
			report(t, planner, build, "x", false, 0)
			report(t, planner, build, "x", true, 10)
		} else {
			report(t, planner, build, "x", true, 10)
		}
		passed, err := planner.Finish(build)
		if err != nil || !passed {
			t.Fatalf("Finish(%s) = %v, %v; want true", build, passed, err)
		}
	}

	plan, err := planner.Plan("build-4", []string{"x"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input=build-4 output=%v reason=real-time quarantine snapshot", plan)
	if len(plan) != 2 || !reflect.DeepEqual(plan[1], []string{"x"}) {
		t.Fatalf("plan = %v, want quarantine shard", plan)
	}
	report(t, planner, "build-4", "x", false, 0)
	report(t, planner, "build-4", "x", false, 0)
	passed, err := planner.Finish("build-4")
	if err != nil || !passed {
		t.Fatalf("quarantined broken build = %v, %v; want passed", passed, err)
	}

	for _, build := range []string{"build-5", "build-6"} {
		plan, err := planner.Plan(build, []string{"x"}, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(plan) != 2 || plan[1][0] != "x" {
			t.Fatalf("%s plan = %v, want x quarantined", build, plan)
		}
		report(t, planner, build, "x", true, 10)
		passed, err := planner.Finish(build)
		if err != nil || !passed {
			t.Fatalf("Finish(%s) = %v, %v", build, passed, err)
		}
	}
	record := planner.store.Snapshot("x")
	t.Logf("output record=%+v reason=second clean releases and clears window", record)
	if record.Quarantined || len(record.Window) != 0 || record.CleanStreak != 0 {
		t.Fatalf("record after release = %+v", record)
	}
}

func TestSnapshotCanFailWhileRealTimeQuarantined(t *testing.T) {
	planner := mustPlanner(t, 1, 1, 1, 1, 1)

	planFour, err := planner.Plan("build-4", []string{"x", "y"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	planThree, err := planner.Plan("build-3", []string{"x"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("build4 snapshot=%v; build3 snapshot=%v", planFour, planThree)

	report(t, planner, "build-3", "x", false, 0)
	report(t, planner, "build-3", "x", true, 1)
	if _, err := planner.Finish("build-3"); err != nil {
		t.Fatalf("build3 Finish error = %v", err)
	}
	report(t, planner, "build-4", "x", false, 0)
	report(t, planner, "build-4", "x", false, 0)
	report(t, planner, "build-4", "y", true, 1)
	passed, err := planner.Finish("build-4")
	if err != nil || passed {
		t.Fatalf("build4 = %v, %v; want failed by snapshot", passed, err)
	}
	record := planner.store.Snapshot("x")
	t.Logf("output record=%+v reason=real-time quarantine ignores broken mark and resets streak", record)
	if !record.Quarantined || record.CleanStreak != 0 {
		t.Fatalf("record = %+v", record)
	}
	if len(record.Window) != 1 || record.Window[0] != history.Flaky {
		t.Fatalf("window = %v, want triggering flaky retained until release", record.Window)
	}
}

func TestReportRetryAndStateErrors(t *testing.T) {
	planner := mustPlanner(t, 5, 2, 2, 1, 3)
	plan, err := planner.Plan("b", []string{"flaky", "broken", "clean"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("input plan output=%v", plan)

	touched := report(t, planner, "b", "flaky", false, 0)
	if touched > 2 {
		t.Fatalf("touched = %d, want <= 2", touched)
	}
	touched = report(t, planner, "b", "flaky", true, 7)
	if touched != 2 {
		t.Fatalf("touched = %d, want 2", touched)
	}
	err = planner.Report("b", "flaky", true, 7)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("after final err = %v, want ErrInvalidState", err)
	}

	report(t, planner, "b", "broken", false, 0)
	report(t, planner, "b", "broken", false, 0)
	err = planner.Report("b", "broken", false, 0)
	if !errors.Is(err, ErrInvalidState) {
		t.Fatalf("after retries err = %v, want ErrInvalidState", err)
	}
	report(t, planner, "b", "clean", true, 3)

	passed, err := planner.Finish("b")
	if err != nil || passed {
		t.Fatalf("Finish = %v, %v; want failed", passed, err)
	}
	record := planner.store.Snapshot("flaky")
	if len(record.Samples) != 1 || record.Samples[0] != 7 {
		t.Fatalf("flaky samples = %v, want [7]", record.Samples)
	}
}

func TestRejectionPrecedence(t *testing.T) {
	planner := mustPlanner(t, 5, 2, 2, 0, 3)
	if _, err := planner.Plan("", []string{"x"}, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid Plan err = %v", err)
	}
	if _, err := planner.Plan("b", []string{"x"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Plan("b", []string{"x"}, 1); !errors.Is(err, ErrBuildExists) {
		t.Fatalf("duplicate build err = %v", err)
	}
	if err := planner.Report("missing", "x", true, 0); !errors.Is(err, ErrBuildNotFound) {
		t.Fatalf("missing build err = %v", err)
	}
	if err := planner.Report("b", "missing", true, 0); !errors.Is(err, ErrTestNotPlanned) {
		t.Fatalf("missing test err = %v", err)
	}
	if _, err := planner.Finish("b"); !errors.Is(err, ErrBuildIncomplete) {
		t.Fatalf("incomplete err = %v", err)
	}
	report(t, planner, "b", "x", true, 0)
	if _, err := planner.Finish("b"); err != nil {
		t.Fatal(err)
	}
	if _, err := planner.Plan("b", []string{"x"}, 1); !errors.Is(err, ErrBuildExists) {
		t.Fatalf("finished plan err = %v", err)
	}
	if err := planner.Report("b", "x", true, 0); !errors.Is(err, ErrBuildFinished) {
		t.Fatalf("finished report err = %v", err)
	}
	if _, err := planner.Finish("b"); !errors.Is(err, ErrBuildFinished) {
		t.Fatalf("finished finish err = %v", err)
	}
}

func TestReportTouchedIndependentOfTotal(t *testing.T) {
	for _, total := range []int{100, 10_000} {
		planner := mustPlanner(t, 5, 2, 2, 0, 3)
		tests := make([]string, total)
		for index := range tests {
			tests[index] = fmt.Sprintf("test-%05d", index)
		}
		if _, err := planner.Plan("large", tests, 256); err != nil {
			t.Fatal(err)
		}
		if err := planner.Report("large", tests[total-1], true, 1); err != nil {
			t.Fatal(err)
		}
		touched := planner.touchedCount()
		t.Logf("input total=%d output touched=%d", total, touched)
		if touched > 2 {
			t.Fatalf("total=%d touched=%d, want <= 2", total, touched)
		}
	}
}

func TestConcurrentOperations(t *testing.T) {
	planner := mustPlanner(t, 5, 2, 2, 0, 3)
	var wg sync.WaitGroup
	for index := 0; index < 32; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			build := fmt.Sprintf("build-%02d", index)
			test := fmt.Sprintf("test-%02d", index)
			if _, err := planner.Plan(build, []string{test}, 1); err != nil {
				t.Errorf("Plan: %v", err)
				return
			}
			if err := planner.Report(build, test, true, index); err != nil {
				t.Errorf("Report: %v", err)
				return
			}
			if _, err := planner.Finish(build); err != nil {
				t.Errorf("Finish: %v", err)
			}
		}(index)
	}
	wg.Wait()
}

type simRecord struct {
	samples []int
	window  []int
	q       bool
	streak  int
}

type simAttempt struct {
	snapshotQ bool
	attempts  int
	final     bool
	passed    bool
	mark      int
	duration  int
}

type simBuild struct {
	name     string
	finished bool
	tests    map[string]*simAttempt
}

type simulator struct {
	w, f, p, r, d int
	records       map[string]*simRecord
	builds        map[string]*simBuild
}

func newSimulator(w, f, p, r, d int) *simulator {
	return &simulator{
		w:       w,
		f:       f,
		p:       p,
		r:       r,
		d:       d,
		records: make(map[string]*simRecord),
		builds:  make(map[string]*simBuild),
	}
}

func simEstimate(samples []int) int {
	total := 0
	for _, value := range samples {
		total += value
	}
	return (total + len(samples) - 1) / len(samples)
}

func (s *simulator) record(name string) *simRecord {
	if record, ok := s.records[name]; ok {
		return record
	}
	record := &simRecord{}
	s.records[name] = record
	return record
}

func (s *simulator) plan(names []string, n int) [][]string {
	known := make([]int, 0)
	for _, name := range names {
		record := s.record(name)
		if !record.q && len(record.samples) > 0 {
			known = append(known, simEstimate(record.samples))
		}
	}
	sort.Ints(known)
	unknown := 1
	if len(known) > 0 {
		unknown = known[(len(known)-1)/2]
	}

	type item struct {
		name string
		est  int
	}
	items := make([]item, 0, len(names))
	quarantined := make([]string, 0)
	for _, name := range names {
		record := s.record(name)
		if record.q {
			quarantined = append(quarantined, name)
			continue
		}
		est := unknown
		if len(record.samples) > 0 {
			est = simEstimate(record.samples)
		}
		items = append(items, item{name: name, est: est})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].est != items[j].est {
			return items[i].est > items[j].est
		}
		return items[i].name < items[j].name
	})

	regular := make([][]string, n)
	sums := make([]int, n)
	for _, candidate := range items {
		target := 0
		for index := 1; index < n; index++ {
			if sums[index] < sums[target] {
				target = index
			}
		}
		regular[target] = append(regular[target], candidate.name)
		sums[target] += candidate.est
	}
	sort.Strings(quarantined)
	if len(quarantined) > 0 {
		regular = append(regular, quarantined)
	}
	return regular
}

func (s *simulator) report(attempt *simAttempt, pass bool, ms int) {
	attempt.attempts++
	if pass {
		attempt.final = true
		attempt.passed = true
		attempt.duration = ms
		if attempt.attempts == 1 {
			attempt.mark = int(history.Clean)
		} else {
			attempt.mark = int(history.Flaky)
		}
		return
	}
	if attempt.attempts == s.r+1 {
		attempt.final = true
		attempt.passed = false
		attempt.mark = int(history.Broken)
	}
}

func (s *simulator) finish(build *simBuild) bool {
	failed := false
	for _, attempt := range build.tests {
		if !attempt.snapshotQ && attempt.mark == int(history.Broken) {
			failed = true
		}
	}
	names := make([]string, 0, len(build.tests))
	for name := range build.tests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		attempt := build.tests[name]
		record := s.record(name)
		if attempt.passed {
			record.samples = append(record.samples, attempt.duration)
			if len(record.samples) > s.d {
				record.samples = append([]int(nil), record.samples[len(record.samples)-s.d:]...)
			}
		}
		if !record.q {
			record.window = append(record.window, attempt.mark)
			if len(record.window) > s.w {
				record.window = append([]int(nil), record.window[len(record.window)-s.w:]...)
			}
			flaky := 0
			for _, mark := range record.window {
				if mark == int(history.Flaky) {
					flaky++
				}
			}
			if flaky >= s.f {
				record.q = true
				record.streak = 0
			}
			continue
		}
		if attempt.mark == int(history.Clean) {
			record.streak++
			if record.streak >= s.p {
				record.q = false
				record.streak = 0
				record.window = nil
			}
		} else {
			record.streak = 0
		}
	}
	return !failed
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(1423))
	universe := []string{"a", "b", "c", "d", "e", "f", "g"}
	for iteration := 0; iteration < 1500; iteration++ {
		t.Run(fmt.Sprintf("sequence-%04d", iteration), func(t *testing.T) {
			w := 1 + rng.Intn(5)
			f := 1 + rng.Intn(w)
			p := 1 + rng.Intn(3)
			r := rng.Intn(3)
			d := 1 + rng.Intn(4)
			planner := mustPlanner(t, w, f, p, r, d)
			sim := newSimulator(w, f, p, r, d)
			buildCount := 1 + rng.Intn(4)
			planned := 0
			finishedPlanner := 0

			t.Logf("input config W=%d F=%d P=%d R=%d D=%d", w, f, p, r, d)
			for step := 0; finishedPlanner < buildCount; step++ {
				if planned < buildCount && (len(sim.builds) == 0 || rng.Intn(3) == 0) {
					names := append([]string(nil), universe...)
					rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
					names = names[:1+rng.Intn(len(names))]
					build := fmt.Sprintf("build-%d-%d", iteration, planned)
					n := 1 + rng.Intn(3)
					want := sim.plan(names, n)
					got, err := planner.Plan(build, names, n)
					if err != nil {
						t.Fatalf("Plan error = %v", err)
					}
					t.Logf("step=%d input Plan(%s,%v,%d) output=%v basis=median greedy snapshot", step, build, names, n, got)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("plan = %v, want %v", got, want)
					}
					tests := make(map[string]*simAttempt, len(names))
					for _, name := range names {
						tests[name] = &simAttempt{snapshotQ: sim.record(name).q}
					}
					sim.builds[build] = &simBuild{name: build, tests: tests}
					planned++
					continue
				}

				open := make([]*simBuild, 0, len(sim.builds))
				for _, build := range sim.builds {
					if !build.finished {
						open = append(open, build)
					}
				}
				if len(open) == 0 {
					continue
				}
				build := open[rng.Intn(len(open))]
				pending := make([]string, 0)
				for name, attempt := range build.tests {
					if !attempt.final {
						pending = append(pending, name)
					}
				}
				if len(pending) == 0 && rng.Intn(5) != 0 {
					wantPassed := sim.finish(build)
					gotPassed, err := planner.Finish(build.name)
					if err != nil {
						t.Fatalf("Finish error = %v", err)
					}
					t.Logf("step=%d input Finish(%s) output=%v basis=snapshot broken=%v real-time update sorted names", step, build.name, gotPassed, !wantPassed)
					if gotPassed != wantPassed {
						t.Fatalf("Finish = %v, want %v", gotPassed, wantPassed)
					}
					finishedPlanner++
					build.finished = true
					continue
				}
				finishedBuilds := 0
				for _, candidate := range sim.builds {
					if candidate.finished {
						finishedBuilds++
					}
				}
				if len(pending) == 0 {
					wantPassed := sim.finish(build)
					gotPassed, err := planner.Finish(build.name)
					if err != nil || gotPassed != wantPassed {
						t.Fatalf("Finish = %v, %v, want %v", gotPassed, err, wantPassed)
					}
					finishedPlanner++
					build.finished = true
					if finishedBuilds > 0 && rng.Intn(2) == 0 {
						var finishedName string
						for name, candidate := range sim.builds {
							if candidate.finished && name != build.name {
								finishedName = name
								break
							}
						}
						if finishedName != "" {
							if _, err := planner.Finish(finishedName); !errors.Is(err, ErrBuildFinished) {
								t.Fatalf("duplicate finish %s err = %v, want finished", finishedName, err)
							}
						}
					}
					continue
				}
				if rng.Intn(8) == 0 {
					if _, err := planner.Finish(build.name); !errors.Is(err, ErrBuildIncomplete) {
						t.Fatalf("incomplete finish err = %v, want incomplete", err)
					}
					t.Logf("step=%d input Finish(%s) output=%v basis=pending=%v", step, build.name, ErrBuildIncomplete, pending)
					continue
				}

				sort.Strings(pending)
				name := pending[rng.Intn(len(pending))]
				attempt := build.tests[name]
				pass := rng.Intn(2) == 0
				ms := rng.Intn(21)
				if err := planner.Report(build.name, name, pass, ms); err != nil {
					t.Fatalf("Report error = %v", err)
				}
				touched := planner.touchedCount()
				sim.report(attempt, pass, ms)
				t.Logf("step=%d input Report(%s,%s,%v,%d) output=touched:%d final:%v basis=attempts:%d R+1:%d", step, build.name, name, pass, ms, touched, attempt.final, attempt.attempts, r+1)
				if touched > 2 {
					t.Fatalf("touched = %d, want <= 2", touched)
				}
			}

			for _, name := range universe {
				record := sim.record(name)
				got := planner.store.Snapshot(name)
				wantWindow := make([]history.Mark, len(record.window))
				for index, mark := range record.window {
					wantWindow[index] = history.Mark(mark)
				}
				t.Logf("final %s model={samples:%v window:%v q:%v streak:%d} actual=%+v", name, record.samples, record.window, record.q, record.streak, got)
				wantSamples := record.samples
				if len(wantSamples) == 0 {
					wantSamples = nil
				}
				if len(wantWindow) == 0 {
					wantWindow = nil
				}
				if !reflect.DeepEqual(got.Samples, wantSamples) || !reflect.DeepEqual(got.Window, wantWindow) || got.Quarantined != record.q || got.CleanStreak != record.streak {
					t.Fatalf("history mismatch for %s: got=%+v want={samples:%v window:%v q:%v streak:%d}", name, got, record.samples, record.window, record.q, record.streak)
				}
			}
		})
	}
}
