package ontology

import (
	"bytes"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

type naiveEntry struct {
	key     string
	created int64
	timer   int64
}

type naiveCleaner struct {
	clock      int64
	min        int64
	max        int64
	maxLife    int64
	capacity   int
	entries    map[string]*naiveEntry
	lastAccess map[string]int64
	cleaned    int64
	timerOps   int64
	lastReason string
}

type modelResult struct {
	cleaned []string
	timer   int64
	err     error
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewSource(20261002))

	for run := 0; run < 2000; run++ {
		minRetention := int64(1 + rng.Intn(20))
		maxRetention := minRetention + int64(rng.Intn(30))
		maxLife := minRetention + int64(rng.Intn(int(maxRetention-minRetention)+25))
		if maxLife < minRetention {
			maxLife = minRetention
		}
		capacity := 1 + rng.Intn(5)

		actual, err := NewIdleStateCleaner(minRetention, maxRetention, maxLife, capacity)
		if err != nil {
			t.Fatalf("run %d: constructor: %v", run, err)
		}
		model := newNaiveCleaner(minRetention, maxRetention, maxLife, capacity)

		var log bytes.Buffer
		fmt.Fprintf(&log, "run=%d Min=%d Max=%d Hmax=%d K=%d\n", run, minRetention, maxRetention, maxLife, capacity)

		opCount := 12 + rng.Intn(30)
		for step := 0; step < opCount; step++ {
			var key []byte
			if rng.Intn(10) != 0 {
				key = []byte(fmt.Sprintf("k%02d", rng.Intn(capacity+3)))
			}
			now := model.clock + int64(rng.Intn(int(maxRetention+maxLife)+20)-5)

			if rng.Intn(4) == 0 {
				got, gotErr := actual.Advance(now)
				want := model.advance(now)
				fmt.Fprintf(&log, "step=%d Advance(now=%d) => cleaned=%v err=%v rationale=%s\n",
					step, now, got, gotErr, model.lastReason)
				if !errorsEqual(gotErr, want.err) || !stringSlicesEqual(got, want.cleaned) {
					t.Fatalf("run %d, step %d: Advance mismatch\n%s", run, step, log.String())
				}
			} else {
				gotCleaned, gotTimer, gotErr := actual.Touch(key, now)
				want := model.touch(key, now)
				fmt.Fprintf(&log, "step=%d Touch(key=%q,now=%d) => cleaned=%v timer=%d err=%v rationale=%s\n",
					step, string(key), now, gotCleaned, gotTimer, gotErr, model.lastReason)
				if !errorsEqual(gotErr, want.err) ||
					!stringSlicesEqual(gotCleaned, want.cleaned) ||
					gotTimer != want.timer {
					t.Fatalf("run %d, step %d: Touch mismatch\n%s", run, step, log.String())
				}
			}

			if actual.clock != model.clock ||
				actual.Cleaned() != model.cleaned ||
				actual.timerOps != model.timerOps ||
				actual.Size() != len(model.entries) {
				t.Fatalf("run %d, step %d: counters mismatch actual=(clock=%d size=%d cleaned=%d ops=%d) model=%s\n%s",
					run, step, actual.clock, actual.Size(), actual.Cleaned(), actual.timerOps,
					model.stateSummary(), log.String())
			}

			for _, ent := range model.entries {
				timer, err := actual.Timer([]byte(ent.key))
				if err != nil || timer != ent.timer {
					t.Fatalf("run %d, step %d: Timer(%q)=%d,%v want %d\n%s",
						run, step, ent.key, timer, err, ent.timer, log.String())
				}
				model.assertInvariant(t, actual, ent.key)
			}
		}

		t.Logf("\n%sfinal=%s", log.String(), model.stateSummary())
	}
}

func TestConcurrentTouchAndAdvance(t *testing.T) {
	cleaner := mustNewCleaner(t, 10, 30, 1_000_000_000_000, 8)
	var wg sync.WaitGroup

	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for step := 0; step < 100; step++ {
				key := []byte(fmt.Sprintf("k%d", worker))
				now := int64(step * 2)
				cleaner.Touch(key, now)
				cleaner.Advance(now)
				_ = cleaner.Size()
				_ = cleaner.Cleaned()
				_ = cleaner.Has(key)
			}
		}(worker)
	}

	wg.Wait()

	if cleaner.Size() > 8 {
		t.Fatalf("size=%d exceeds capacity", cleaner.Size())
	}
}

func newNaiveCleaner(minRetention, maxRetention, maxLife int64, capacity int) *naiveCleaner {
	return &naiveCleaner{
		min:        minRetention,
		max:        maxRetention,
		maxLife:    maxLife,
		capacity:   capacity,
		entries:    make(map[string]*naiveEntry),
		lastAccess: make(map[string]int64),
	}
}

func (m *naiveCleaner) touch(key []byte, now int64) modelResult {
	if len(key) == 0 || now < 0 || now > 1_000_000_000_000 {
		m.lastReason = "invalid key or now"
		return modelResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		m.lastReason = fmt.Sprintf("rollback now=%d < T=%d", now, m.clock)
		return modelResult{err: ErrClockRollback}
	}

	keyString := string(key)
	due := 0
	for _, ent := range m.entries {
		if ent.timer <= now {
			due++
		}
	}
	existing := m.entries[keyString]
	present := existing != nil && existing.timer > now
	if !present && len(m.entries)-due+1 > m.capacity {
		m.lastReason = fmt.Sprintf("capacity: size=%d due=%d, projected=%d > K=%d",
			len(m.entries), due, len(m.entries)-due+1, m.capacity)
		return modelResult{err: ErrCapacityLimit}
	}

	cleaned := m.cleanDue(now)
	m.clock = now

	ent := m.entries[keyString]
	if ent == nil {
		timer := min(now+m.max, now+m.maxLife)
		m.entries[keyString] = &naiveEntry{key: keyString, created: now, timer: timer}
		m.lastAccess[keyString] = now
		m.timerOps++
		m.lastReason = fmt.Sprintf("new entry created=%d timer=min(%d,%d)=%d", now, now+m.max, now+m.maxLife, timer)
		return modelResult{cleaned: cleaned, timer: timer}
	}

	m.lastAccess[keyString] = now
	oldTimer := ent.timer
	if now+m.min > oldTimer {
		newTimer := min(now+m.max, ent.created+m.maxLife)
		m.lastReason = fmt.Sprintf("remaining insufficient: now+Min=%d > old=%d; candidate=min(%d,%d)=%d",
			now+m.min, oldTimer, now+m.max, ent.created+m.maxLife, newTimer)
		if newTimer != oldTimer {
			ent.timer = newTimer
			m.timerOps++
			m.lastReason += "; timer replaced"
		} else {
			m.lastReason += "; candidate unchanged"
		}
	} else {
		m.lastReason = fmt.Sprintf("reuse timer: now+Min=%d <= old=%d", now+m.min, oldTimer)
	}

	return modelResult{cleaned: cleaned, timer: ent.timer}
}

func (m *naiveCleaner) advance(now int64) modelResult {
	if now < 0 || now > 1_000_000_000_000 {
		m.lastReason = "invalid now"
		return modelResult{err: ErrInvalidArgument}
	}
	if now < m.clock {
		m.lastReason = fmt.Sprintf("rollback now=%d < T=%d", now, m.clock)
		return modelResult{err: ErrClockRollback}
	}

	cleaned := m.cleanDue(now)
	m.clock = now
	m.lastReason = fmt.Sprintf("advance T=%d; expired entries removed in (timer,key) order", now)
	return modelResult{cleaned: cleaned}
}

func (m *naiveCleaner) cleanDue(now int64) []string {
	due := make([]*naiveEntry, 0)
	for _, ent := range m.entries {
		if ent.timer <= now {
			due = append(due, ent)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].timer != due[j].timer {
			return due[i].timer < due[j].timer
		}
		return due[i].key < due[j].key
	})

	cleaned := make([]string, 0, len(due))
	for _, ent := range due {
		delete(m.entries, ent.key)
		cleaned = append(cleaned, ent.key)
		m.cleaned++
	}
	return cleaned
}

func (m *naiveCleaner) stateSummary() string {
	entries := make([]string, 0, len(m.entries))
	for _, ent := range m.entries {
		entries = append(entries, fmt.Sprintf("%s{created=%d,timer=%d}", ent.key, ent.created, ent.timer))
	}
	sort.Strings(entries)
	return fmt.Sprintf("T=%d size=%d cleaned=%d timerOps=%d entries=%v",
		m.clock, len(m.entries), m.cleaned, m.timerOps, entries)
}

func (m *naiveCleaner) assertInvariant(t *testing.T, cleaner *IdleStateCleaner, key string) {
	t.Helper()
	ent := m.entries[key]
	lastAccess := m.lastAccess[key]
	lower := min(lastAccess+m.min, ent.created+m.maxLife)
	upper := min(lastAccess+m.max, ent.created+m.maxLife)
	if ent.timer < lower || ent.timer > upper {
		t.Fatalf("invariant for %q: timer=%d outside [%d,%d]", key, ent.timer, lower, upper)
	}
	if ent.timer <= m.clock || ent.timer <= cleaner.clock {
		t.Fatalf("timer %d is not greater than clocks model=%d actual=%d", ent.timer, m.clock, cleaner.clock)
	}
}

func errorsEqual(left, right error) bool {
	return left == right
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
