// Package api is the public facade over vcache/ver.
package api

import (
	"errors"
	"fmt"

	"ontology/vcache"
	"ontology/ver"
)

// Re-exported sentinel errors, distinguishable via errors.Is.
var (
	ErrEmptyGroup  = ver.ErrEmptyGroup
	ErrEmptyKey    = ver.ErrEmptyKey
	ErrKeyConflict = ver.ErrKeyConflict
)

// ErrSelfCheck is returned (wrapped) when SelfCheck finds a violation.
var ErrSelfCheck = errors.New("api: self-check failed")

type Service struct {
	st *ver.Store
	vc *vcache.Cache
}

func New() *Service {
	st := ver.NewStore()
	return &Service{st: st, vc: vcache.New(st)}
}

func (s *Service) Write(group, key string, val int64) error {
	return s.st.Write(group, key, val)
}

func (s *Service) ReadG(group string) (int64, error) {
	if group == "" {
		return 0, ErrEmptyGroup
	}
	return s.vc.ReadG(group), nil
}

func (s *Service) ReadTotal() int64 { return s.vc.ReadTotal() }

// View returns per-group sums and the grand total.
func (s *Service) View() (map[string]int64, int64) { return s.vc.View() }

// SelfCheck verifies the four invariants on built-in write sequences,
// using fresh internal instances so it is side-effect free.
func (s *Service) SelfCheck() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrSelfCheck, fmt.Sprintf(format, args...))
	}
	// Invariant 3 (monotonic versions): each accepted write bumps the
	// total stamp by exactly 1 and never lets any stamp regress.
	st := ver.NewStore()
	for i, w := range [][3]any{{"g0", "a", 5}, {"g0", "b", 3}, {"g1", "c", 7}, {"g0", "a", 9}} {
		prevT, prevG := st.TotalStamp(), st.Stamp(w[0].(string))
		if err := st.Write(w[0].(string), w[1].(string), int64(w[2].(int))); err != nil {
			return fail("inv3: write %d rejected: %v", i, err)
		}
		if st.TotalStamp() != prevT+1 || st.Stamp(w[0].(string)) != prevG+1 {
			return fail("inv3: stamp not monotonic at write %d", i)
		}
	}
	// Invariant 4 (no trace on rejection): three distinct rejections.
	before, totBefore := st.SumAll(), st.TotalStamp()
	for _, w := range [][3]any{{"", "x", 1}, {"g0", "", 1}, {"g1", "a", 1}} {
		if err := st.Write(w[0].(string), w[1].(string), int64(w[2].(int))); err == nil {
			return fail("inv4: invalid write %+v accepted", w)
		}
	}
	if st.SumAll() != before || st.TotalStamp() != totBefore {
		return fail("inv4: rejected write changed state")
	}
	// Invariants 1+2 (match batch recompute; cache refreshed to current):
	// the eight-step sequence from NOTES.md must read 5/8/15/22, and a
	// second View pass (now cache-warm) must still equal batch recompute.
	svc := New()
	must := func(err error) error {
		if err != nil {
			return fail("inv1: unexpected write error: %v", err)
		}
		return nil
	}
	if err := must(svc.Write("g0", "a", 5)); err != nil {
		return err
	}
	got := make([]int64, 0, 4)
	if v, err := svc.ReadG("g0"); err == nil {
		got = append(got, v)
	}
	if err := must(svc.Write("g0", "b", 3)); err != nil {
		return err
	}
	if v, err := svc.ReadG("g0"); err == nil {
		got = append(got, v)
	}
	if err := must(svc.Write("g1", "c", 7)); err != nil {
		return err
	}
	got = append(got, svc.ReadTotal())
	if err := must(svc.Write("g0", "b", 10)); err != nil {
		return err
	}
	got = append(got, svc.ReadTotal())
	want := []int64{5, 8, 15, 22}
	for i := range want {
		if got[i] != want[i] {
			return fail("inv1: step read %d = %d, want %d", i, got[i], want[i])
		}
	}
	groups, total := svc.View()
	if groups["g0"] != 15 || groups["g1"] != 7 || total != 22 {
		return fail("inv1/2: warm View %v/%d, want {g0:15 g1:7}/22", groups, total)
	}
	return nil
}
