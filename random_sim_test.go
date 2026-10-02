package ontology

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveEntry struct {
	key     string
	created int64
	timer   int64
}

type naiveCleaner struct {
	min      int64
	max      int64
	hmax     int64
	capacity int
	now      int64
	entries  []*naiveEntry
	cleaned  int64
	timerOps int64
}

type modelResult struct {
	cleaned []string
	timer   int64
	err     error
	reason  string
}

func TestRandomReplayMatchesNaiveSimulation(t *testing.T) {
	for sequence := 0; sequence < 2000; sequence++ {
		rng := rand.New(rand.NewSource(int64(sequence + 1)))
		min := int64(rng.Intn(5) + 1)
		max := min + int64(rng.Intn(7))
		hmax := min + int64(rng.Intn(13))
		capacity := rng.Intn(5) + 1

		t.Run("", func(t *testing.T) {
			actual, err := NewIdleCleaner(min, max, hmax, capacity)
			if err != nil {
				t.Fatalf("constructor: %v", err)
			}
			model := &naiveCleaner{
				min:      min,
				max:      max,
				hmax:     hmax,
				capacity: capacity,
			}

			for step := 0; step < 16; step++ {
				key := []string{"", "a", "b", "c"}[rng.Intn(4)]
				now := model.now
				switch rng.Intn(10) {
				case 0:
					now = 0
				case 1:
					if model.now > 0 {
						now = rng.Int63n(model.now)
					}
				case 2:
					now = 1_000_000_000_001
				default:
					now += int64(rng.Intn(9))
				}

				if rng.Intn(4) == 0 {
					want := model.advance(now)
					gotCleaned, gotErr := actual.Advance(now)
					t.Logf("seq=%d step=%d input=Advance(now=%d) output=(cleaned=%v,err=%v) reason=%s", sequence, step, now, gotCleaned, gotErr, want.reason)
					assertResult(t, gotCleaned, 0, gotErr, want)
				} else {
					want := model.touch(key, now)
					gotCleaned, gotTimer, gotErr := actual.Touch(key, now)
					t.Logf("seq=%d step=%d input=Touch(key=%q,now=%d) output=(cleaned=%v,timer=%d,err=%v) reason=%s", sequence, step, key, now, gotCleaned, gotTimer, gotErr, want.reason)
					assertResult(t, gotCleaned, gotTimer, gotErr, want)
				}

				assertState(t, actual, model)
			}
		})
	}
}

func TestConstructorRejectsInvalidBounds(t *testing.T) {
	cases := []struct {
		min, max, hmax int64
		capacity       int
	}{
		{0, 10, 10, 1},
		{11, 10, 11, 1},
		{1, 1_000_000_000_001, 1_000_000_000_001, 1},
		{10, 10, 9, 1},
		{1, 10, 1_000_000_000_001, 1},
		{1, 10, 10, 0},
		{1, 10, 10, 1_000_001},
	}
	for _, tc := range cases {
		if _, err := NewIdleCleaner(tc.min, tc.max, tc.hmax, tc.capacity); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("NewIdleCleaner(%d,%d,%d,%d)=%v; reason=constructor bounds", tc.min, tc.max, tc.hmax, tc.capacity, err)
		}
	}
}

func (m *naiveCleaner) touch(key string, now int64) modelResult {
	if key == "" || now < 0 || now > 1_000_000_000_000 {
		return modelResult{err: ErrInvalidArgument, reason: "invalid key or now; state unchanged"}
	}
	if now < m.now {
		return modelResult{err: ErrClockRolledBack, reason: "now < T; state unchanged"}
	}

	due := 0
	present := false
	for _, entry := range m.entries {
		if entry.timer <= now {
			due++
		}
		if entry.key == key && entry.timer > now {
			present = true
		}
	}
	if !present && len(m.entries)-due+1 > m.capacity {
		return modelResult{err: ErrCapacityExceeded, reason: "absent after due releases, size-due+1 > K; state unchanged"}
	}

	cleaned := m.cleanDue(now)
	m.now = now

	entry := m.find(key)
	if entry == nil {
		timer := min64(now+m.max, now+m.hmax)
		m.entries = append(m.entries, &naiveEntry{key: key, created: now, timer: timer})
		m.timerOps++
		return modelResult{cleaned: cleaned, timer: timer, reason: "new entry; one registration"}
	}

	if now+m.min > entry.timer {
		nextTimer := min64(now+m.max, entry.created+m.hmax)
		if nextTimer == entry.timer {
			return modelResult{cleaned: cleaned, timer: entry.timer, reason: "refresh needed but lifetime-clamped timer equals current; no registration"}
		}
		entry.timer = nextTimer
		m.timerOps++
		return modelResult{cleaned: cleaned, timer: nextTimer, reason: "now+Min > current; one replacement registration"}
	}

	return modelResult{cleaned: cleaned, timer: entry.timer, reason: "now+Min <= current; existing timer retained"}
}

func (m *naiveCleaner) advance(now int64) modelResult {
	if now < 0 || now > 1_000_000_000_000 {
		return modelResult{err: ErrInvalidArgument, reason: "now out of range; state unchanged"}
	}
	if now < m.now {
		return modelResult{err: ErrClockRolledBack, reason: "now < T; state unchanged"}
	}

	cleaned := m.cleanDue(now)
	m.now = now
	return modelResult{cleaned: cleaned, reason: "all timer <= now popped in (timer,key) order; T=now"}
}

func (m *naiveCleaner) cleanDue(now int64) []string {
	due := make([]*naiveEntry, 0)
	remaining := m.entries[:0]
	for _, entry := range m.entries {
		if entry.timer <= now {
			due = append(due, entry)
		} else {
			remaining = append(remaining, entry)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].timer != due[j].timer {
			return due[i].timer < due[j].timer
		}
		return due[i].key < due[j].key
	})

	cleaned := make([]string, 0, len(due))
	for _, entry := range due {
		cleaned = append(cleaned, entry.key)
		m.cleaned++
	}
	m.entries = remaining
	return cleaned
}

func (m *naiveCleaner) find(key string) *naiveEntry {
	for _, entry := range m.entries {
		if entry.key == key {
			return entry
		}
	}
	return nil
}

func assertResult(t *testing.T, cleaned []string, timer int64, err error, want modelResult) {
	t.Helper()
	if !reflect.DeepEqual(cleaned, want.cleaned) {
		t.Fatalf("cleaned=%v want=%v", cleaned, want.cleaned)
	}
	if !errors.Is(err, want.err) {
		t.Fatalf("err=%v want=%v", err, want.err)
	}
	if timer != want.timer {
		t.Fatalf("timer=%d want=%d", timer, want.timer)
	}
}

func assertState(t *testing.T, actual *IdleCleaner, model *naiveCleaner) {
	t.Helper()
	if actual.now != model.now || actual.Size() != len(model.entries) ||
		actual.Cleaned() != model.cleaned || actual.timerOps != model.timerOps {
		t.Fatalf("state T=%d size=%d cleaned=%d ops=%d, want T=%d size=%d cleaned=%d ops=%d",
			actual.now, actual.Size(), actual.Cleaned(), actual.timerOps,
			model.now, len(model.entries), model.cleaned, model.timerOps)
	}

	for _, want := range model.entries {
		got := actual.items[want.key]
		if got == nil {
			t.Fatalf("actual missing key %q", want.key)
		}
		if got.created != want.created || got.timer != want.timer {
			t.Fatalf("key %q actual=(created=%d,timer=%d) want=(created=%d,timer=%d)",
				want.key, got.created, got.timer, want.created, want.timer)
		}
		if got.timer <= actual.now {
			t.Fatalf("invariant failed: key %q timer=%d <= T=%d", want.key, got.timer, actual.now)
		}
	}

	if actual.timers.Len() != len(actual.items) {
		t.Fatalf("heap entries=%d map entries=%d; every key must have one timer", actual.timers.Len(), len(actual.items))
	}
}
