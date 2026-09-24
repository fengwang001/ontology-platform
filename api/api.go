// Package api is the public surface for hot-key detection and re-sharding:
// construction, feeding events, reading counters and a self-check.
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/route"
	"ontology/shard"
)

// Event is one CDC event. Base is the base-routing shard.
type Event struct {
	Key  string
	Base int
}

// Decidable sentinel errors; all four are distinct values.
var (
	ErrInvalidShards  = errors.New("api: number of base shards S must be >= 1")
	ErrInvalidThresh  = errors.New("api: hot threshold T must be >= 1")
	ErrBaseOutOfRange = errors.New("api: event Base out of range [0, S)")
	ErrEmptyKey       = errors.New("api: event Key must be non-empty")
)

// System is the hot-key detection and re-sharding system.
type System struct {
	S, T int
	rt   *route.Router
}

// New constructs a System with S base shards and hot threshold T.
func New(S, T int) (*System, error) {
	if S <= 0 {
		return nil, ErrInvalidShards
	}
	if T <= 0 {
		return nil, ErrInvalidThresh
	}
	return &System{S: S, T: T, rt: route.NewRouter(shard.New(S, T))}, nil
}

// Feed applies a batch. Every event is validated before any state changes,
// so one invalid event rejects the whole batch and leaves no trace; the
// system stays usable afterwards.
func (s *System) Feed(evs []Event) error {
	for _, e := range evs {
		if e.Key == "" {
			return ErrEmptyKey
		}
		if e.Base < 0 || e.Base >= s.S {
			return ErrBaseOutOfRange
		}
	}
	for _, e := range evs {
		s.rt.Apply(e.Key, e.Base)
	}
	return nil
}

// Counts returns a fresh snapshot of cnt ordered by shard number.
func (s *System) Counts() []int64 { return s.rt.Snapshot() }

// IsHot reports whether key is a permanently hot key.
func (s *System) IsHot(key string) bool { return s.rt.IsHot(key) }

// Dedicated returns key's dedicated shard once it has migrated.
func (s *System) Dedicated(key string) (int, bool) { return s.rt.Dedicated(key) }

// canonical is the built-in sequence for S=2, T=3.
func canonical() []Event {
	return []Event{
		{"a", 0}, {"a", 0}, {"a", 0}, {"b", 1},
		{"a", 0}, {"a", 0}, {"c", 1}, {"a", 0},
	}
}

// naiveReference is an independent brute-force simulation of the routing
// rules, event by event.
func naiveReference(S, T int, evs []Event) []int64 {
	cnt := make([]int64, S)
	hot, ded, keyBase := map[string]bool{}, map[string]int{}, map[string]int64{}
	for _, e := range evs {
		if hot[e.Key] {
			cnt[ded[e.Key]]++
			continue
		}
		cnt[e.Base]++
		keyBase[e.Key]++
		if keyBase[e.Key] >= int64(T) {
			ded[e.Key] = S + len(ded)
			hot[e.Key] = true
			cnt = append(cnt, 0)
		}
	}
	return cnt
}

// SelfCheck verifies the four invariants on the built-in sequence:
// migration atomicity (trigger event on base, later events on dedicated),
// conservation, agreement with the naive reference and no-trace failure; it
// also proves O(1) migrated-key lookup.
func (s *System) SelfCheck() error {
	sys, err := New(2, 3)
	if err != nil {
		return err
	}
	evs := canonical()
	if err := sys.Feed(evs); err != nil {
		return err
	}
	got, want := sys.Counts(), []int64{3, 2, 3}
	if !reflect.DeepEqual(got, want) || !sys.IsHot("a") {
		return fmt.Errorf("selfcheck: counts %v, want %v", got, want)
	}
	if d, ok := sys.Dedicated("a"); !ok || d != 2 {
		return fmt.Errorf("selfcheck: a dedicated = (%d,%v), want 2", d, ok)
	}
	var sum int64
	for _, c := range got {
		sum += c
	}
	if sum != int64(len(evs)) {
		return fmt.Errorf("selfcheck: total %d, want %d", sum, len(evs))
	}
	if !reflect.DeepEqual(got, naiveReference(2, 3, evs)) {
		return errors.New("selfcheck: counts diverge from naive reference")
	}
	before := sys.Counts()
	if err := sys.Feed([]Event{{Key: "a", Base: 0}, {Key: "", Base: 0}}); !errors.Is(err, ErrEmptyKey) {
		return fmt.Errorf("selfcheck: reject err = %v, want ErrEmptyKey", err)
	}
	if !reflect.DeepEqual(sys.Counts(), before) {
		return errors.New("selfcheck: rejected batch changed state")
	}
	if !shard.LookupCostStaysConstant() {
		return errors.New("selfcheck: migrated-key lookup is not O(1)")
	}
	return nil
}
