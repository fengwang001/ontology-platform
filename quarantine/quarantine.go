package quarantine

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/history"
	"ontology/shard"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrBuildExists     = errors.New("build already exists")
	ErrBuildNotFound   = errors.New("build not found")
	ErrBuildFinished   = errors.New("build is finished")
	ErrTestNotPlanned  = errors.New("test not in plan")
	ErrInvalidState    = errors.New("invalid report state")
	ErrBuildIncomplete = errors.New("build is incomplete")
)

type Planner struct {
	mu      sync.RWMutex
	window  int
	flaky   int
	clean   int
	retries int
	samples int
	store   *history.Store
	builds  map[string]*buildState
	touched int
}

type buildState struct {
	finished bool
	tests    map[string]*testState
}

type testState struct {
	attempts    int
	final       bool
	passed      bool
	mark        history.Mark
	duration    int
	quarantined bool
}

func New(windowSize, flakyThreshold, cleanReleases, retries, samples int) (*Planner, error) {
	if windowSize < 1 || windowSize > 50 ||
		flakyThreshold < 1 || flakyThreshold > windowSize ||
		cleanReleases < 1 || cleanReleases > 20 ||
		retries < 0 || retries > 3 ||
		samples < 1 || samples > 10 {
		return nil, fmt.Errorf("quarantine: planner options are invalid: %w", ErrInvalidArgument)
	}
	return &Planner{
		window:  windowSize,
		flaky:   flakyThreshold,
		clean:   cleanReleases,
		retries: retries,
		samples: samples,
		store:   history.New(samples),
		builds:  make(map[string]*buildState),
	}, nil
}

func (p *Planner) Plan(build string, tests []string, shardCount int) ([][]string, error) {
	if build == "" || len(tests) < 1 || len(tests) > 10_000 || shardCount < 1 || shardCount > 256 {
		return nil, fmt.Errorf("quarantine: plan arguments are invalid: %w", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(tests))
	for _, name := range tests {
		if name == "" {
			return nil, fmt.Errorf("quarantine: empty test name: %w", ErrInvalidArgument)
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("quarantine: duplicate test name %q: %w", name, ErrInvalidArgument)
		}
		seen[name] = struct{}{}
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.builds[build]; ok {
		return nil, fmt.Errorf("quarantine: build %q: %w", build, ErrBuildExists)
	}

	cases := make([]shard.Case, 0, len(tests))
	states := make(map[string]*testState, len(tests))
	for _, name := range tests {
		record := p.store.Snapshot(name)
		cases = append(cases, shard.Case{
			Name:        name,
			Samples:     record.Samples,
			Quarantined: record.Quarantined,
		})
		states[name] = &testState{quarantined: record.Quarantined}
	}
	plan := shard.Build(cases, shardCount)
	result := append([][]string{}, plan.Regular...)
	if len(plan.Quarantine) > 0 {
		result = append(result, append([]string{}, plan.Quarantine...))
	}
	p.builds[build] = &buildState{tests: states}
	return result, nil
}

func (p *Planner) Report(build, test string, pass bool, ms int) error {
	if build == "" || test == "" || ms < 0 || ms > 1_000_000_000 {
		return fmt.Errorf("quarantine: report arguments are invalid: %w", ErrInvalidArgument)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.touched = 0
	state, ok := p.builds[build]
	p.touched++
	if !ok {
		return fmt.Errorf("quarantine: build %q: %w", build, ErrBuildNotFound)
	}
	if state.finished {
		return fmt.Errorf("quarantine: build %q: %w", build, ErrBuildFinished)
	}
	attempt, ok := state.tests[test]
	p.touched++
	if !ok {
		return fmt.Errorf("quarantine: test %q: %w", test, ErrTestNotPlanned)
	}
	if attempt.final {
		return fmt.Errorf("quarantine: test %q already finished: %w", test, ErrInvalidState)
	}

	attempt.attempts++
	if pass {
		attempt.final = true
		attempt.passed = true
		attempt.duration = ms
		if attempt.attempts == 1 {
			attempt.mark = history.Clean
		} else {
			attempt.mark = history.Flaky
		}
		return nil
	}

	if attempt.attempts == p.retries+1 {
		attempt.final = true
		attempt.passed = false
		attempt.mark = history.Broken
	}
	return nil
}

func (p *Planner) touchedCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.touched
}

func (p *Planner) Finish(build string) (bool, error) {
	if build == "" {
		return false, fmt.Errorf("quarantine: finish arguments are invalid: %w", ErrInvalidArgument)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.builds[build]
	if !ok {
		return false, fmt.Errorf("quarantine: build %q: %w", build, ErrBuildNotFound)
	}
	if state.finished {
		return false, fmt.Errorf("quarantine: build %q: %w", build, ErrBuildFinished)
	}
	for _, attempt := range state.tests {
		if !attempt.final {
			return false, fmt.Errorf("quarantine: build %q has unfinished tests: %w", build, ErrBuildIncomplete)
		}
	}

	failed := false
	for _, attempt := range state.tests {
		if !attempt.quarantined && attempt.mark == history.Broken {
			failed = true
		}
	}

	names := make([]string, 0, len(state.tests))
	for name := range state.tests {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		attempt := state.tests[name]
		p.store.Finish(name, history.Result{
			Mark:     attempt.mark,
			Duration: attempt.duration,
			Passed:   attempt.passed,
		}, p.window, p.flaky, p.clean)
	}
	state.finished = true
	return !failed, nil
}
