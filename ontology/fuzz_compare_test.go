package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type referenceTimer struct {
	id     string
	period uint64
	slack  uint64
	next   uint64
}

type referenceCoalescer struct {
	gap     uint64
	batch   int
	timers  map[string]*referenceTimer
	last    uint64
	hasLast bool
	stats   Stats
}

func newReferenceCoalescer(gap uint64, batch int) *referenceCoalescer {
	return &referenceCoalescer{
		gap:    gap,
		batch:  batch,
		timers: map[string]*referenceTimer{},
	}
}

func (r *referenceCoalescer) add(id string, period, slack, next uint64) {
	r.timers[id] = &referenceTimer{id: id, period: period, slack: slack, next: next}
}

func (r *referenceCoalescer) remove(id string) {
	delete(r.timers, id)
}

func (r *referenceCoalescer) nextAt() (uint64, bool) {
	if len(r.timers) == 0 {
		return 0, false
	}
	earliest := ^uint64(0)
	for _, t := range r.timers {
		if deadline := t.next + t.slack; deadline < earliest {
			earliest = deadline
		}
	}
	if r.hasLast && r.last+r.gap > earliest {
		return r.last + r.gap, true
	}
	return earliest, true
}

func (r *referenceCoalescer) wake() (WakeResult, string) {
	at, ok := r.nextAt()
	if !ok {
		return WakeResult{}, "no timers"
	}

	candidates := make([]*referenceTimer, 0)
	for _, t := range r.timers {
		if t.next <= at {
			candidates = append(candidates, t)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		leftDeadline := candidates[i].next + candidates[i].slack
		rightDeadline := candidates[j].next + candidates[j].slack
		return leftDeadline < rightDeadline || (leftDeadline == rightDeadline && candidates[i].id < candidates[j].id)
	})

	fireCount := r.batch
	if fireCount > len(candidates) {
		fireCount = len(candidates)
	}
	result := WakeResult{At: at, Fired: []FiredEvent{}, Left: len(candidates) - fireCount}
	ids := make([]string, 0, len(candidates))
	for _, t := range candidates {
		ids = append(ids, fmt.Sprintf("%s@%d+%d", t.id, t.next, t.slack))
	}
	for i := 0; i < fireCount; i++ {
		t := candidates[i]
		deadline := t.next + t.slack
		var late uint64
		if at > deadline {
			late = at - deadline
		}
		skips := (at - t.next) / t.period
		result.Fired = append(result.Fired, FiredEvent{ID: t.id, Late: late, Skip: skips})
		t.next += (skips + 1) * t.period
		r.stats.FiredCount++
		if late > 0 {
			r.stats.LateCount++
		}
		r.stats.SkippedCount += skips
	}
	r.last = at
	r.hasLast = true
	r.stats.WakeCount++

	reason := fmt.Sprintf("at=%d orderedCandidates=%v fired=%d left=%d", at, ids, fireCount, result.Left)
	return result, reason
}

func (r *referenceCoalescer) advanceTo(t uint64) []WakeResult {
	results := []WakeResult{}
	var time uint64
	for {
		for {
			at, ok := r.nextAt()
			if !ok || at > time {
				break
			}
			result, _ := r.wake()
			results = append(results, result)
			if uint64(len(results)) == maxAdvanceWakes {
				return results
			}
		}
		if time == t {
			return results
		}
		time++
	}
}

func TestRandomDifferential2000(t *testing.T) {
	for seed := int64(0); seed < 2000; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			gap := uint64(rng.Intn(7))
			batch := 1 + rng.Intn(4)
			c, err := NewCoalescer(gap, batch)
			if err != nil {
				t.Fatal(err)
			}
			ref := newReferenceCoalescer(gap, batch)
			var log strings.Builder
			fmt.Fprintf(&log, "input: New(g=%d,B=%d)\n", gap, batch)

			nextID := 0
			addTimer := func() {
				id := fmt.Sprintf("t%02d", nextID)
				nextID++
				period := uint64(1 + rng.Intn(8))
				slack := uint64(rng.Intn(int(period)))
				minimum := uint64(0)
				if ref.hasLast {
					minimum = ref.last
				}
				next := minimum + uint64(rng.Intn(13))
				actualErr := c.Add(id, period, slack, next)
				if actualErr != nil {
					t.Fatalf("Add(%s P=%d s=%d n=%d): %v\n%s", id, period, slack, next, actualErr, log.String())
				}
				ref.add(id, period, slack, next)
				fmt.Fprintf(&log, "input/output: Add(%s,P=%d,s=%d,n=%d)=ok; basis=id=%q period=%d slack=%d nominal=%d\n", id, period, slack, next, id, period, slack, next)
			}

			initial := rng.Intn(5)
			for i := 0; i < initial; i++ {
				addTimer()
			}

			compareWake := func(label string, actual WakeResult, actualErr error, expected WakeResult) {
				t.Helper()
				if actualErr != nil || !wakeResultEqual(actual, expected) {
					t.Fatalf("%s = (%+v,%v), want %+v\n%s", label, actual, actualErr, expected, log.String())
				}
				fmt.Fprintf(&log, "output: %s=%+v\n", label, actual)
			}

			for step := 0; step < 50; step++ {
				choice := rng.Intn(10)
				switch {
				case choice < 3 && nextID < 20:
					addTimer()
				case choice >= 3 && choice < 5 && len(ref.timers) > 0:
					ids := make([]string, 0, len(ref.timers))
					for id := range ref.timers {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					id := ids[rng.Intn(len(ids))]
					if err := c.Remove(id); err != nil {
						t.Fatalf("Remove(%s): %v\n%s", id, err, log.String())
					}
					ref.remove(id)
					fmt.Fprintf(&log, "input/output: Remove(%s)=ok; basis=id absent from e and candidates\n", id)
				case choice == 5:
					actual, actualErr := c.Next()
					expected, ok := ref.nextAt()
					if (actualErr == nil) != ok || (ok && actual != expected) {
						t.Fatalf("Next()=(%d,%v), want (%d,%v)\n%s", actual, actualErr, expected, ok, log.String())
					}
					fmt.Fprintf(&log, "output: Next()=(%d,%v); basis=min deadline adjusted by last+g\n", actual, actualErr)
				case choice == 6:
					actual, actualErr := c.Wake()
					expected, reason := ref.wake()
					if actualErr != nil {
						if len(ref.timers) != 0 {
							t.Fatalf("Wake(): %v\n%s", actualErr, log.String())
						}
						fmt.Fprintf(&log, "input/output: Wake()=%v; basis=no timers\n", actualErr)
					} else {
						compareWake("Wake", actual, nil, expected)
						fmt.Fprintf(&log, "basis: %s\n", reason)
					}
				default:
					minimum := uint64(0)
					if ref.hasLast {
						minimum = ref.last
					}
					target := minimum + uint64(rng.Intn(18))
					actual, actualErr := c.AdvanceTo(target)
					if len(ref.timers) == 0 {
						if !errors.Is(actualErr, ErrNoTimers) {
							t.Fatalf("AdvanceTo(%d) error=%v, want ErrNoTimers\n%s", target, actualErr, log.String())
						}
						fmt.Fprintf(&log, "input/output: AdvanceTo(%d)=%v; basis=no timers\n", target, actualErr)
						break
					}
					if actualErr != nil {
						t.Fatalf("AdvanceTo(%d): %v\n%s", target, actualErr, log.String())
					}
					expected := ref.advanceTo(target)
					if len(actual) != len(expected) {
						t.Fatalf("AdvanceTo(%d) len=%d, want %d\n%s", target, len(actual), len(expected), log.String())
					}
					for i := range actual {
						if !wakeResultEqual(actual[i], expected[i]) {
							t.Fatalf("AdvanceTo(%d)[%d]=%+v, want %+v\n%s", target, i, actual[i], expected[i], log.String())
						}
					}
					fmt.Fprintf(&log, "input/output: AdvanceTo(%d)=%+v; basis=checked every integer time\n", target, actual)
				}
			}

			if len(c.timers) != len(ref.timers) {
				t.Fatalf("timer count = %d, want %d\n%s", len(c.timers), len(ref.timers), log.String())
			}
			for id, expected := range ref.timers {
				actual := c.timers[id]
				if actual == nil || actual.period != expected.period || actual.slack != expected.slack || actual.next != expected.next || actual.deadline != expected.next+expected.slack {
					t.Fatalf("timer %s = %+v, want %+v\n%s", id, actual, expected, log.String())
				}
			}
			if c.last != ref.last || c.hasLast != ref.hasLast || c.stats != ref.stats {
				t.Fatalf("state=(last=%d,has=%v,stats=%+v), want=(last=%d,has=%v,stats=%+v)\n%s", c.last, c.hasLast, c.stats, ref.last, ref.hasLast, ref.stats, log.String())
			}
			t.Logf("input/output/judgment log:\n%sfinal state: timers=%d last=%d stats=%+v", log.String(), len(ref.timers), ref.last, ref.stats)
		})
	}
}
