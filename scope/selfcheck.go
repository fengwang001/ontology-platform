package scope

import (
	"errors"
	"fmt"
	"math/rand/v2"

	"ontology/sym"
)

var kinds = [2]sym.T{sym.Int, sym.Bool}

func (s *Stack) reset() {
	s.mu.Lock()
	s.frames = []*sym.Scope{sym.NewScope()}
	s.mu.Unlock()
}

// SelfCheck replays built-in operation sequences on s and verifies the four
// specification invariants:
//  1. Lookup agrees with the naive innermost->outermost scan;
//  2. an inner same-name binding shadows without overwriting the outer one;
//  3. a same-scope duplicate Declare is rejected and keeps the first binding;
//  4. every rejected operation fails wholesale without mutating state.
//
// It resets s before running and again at the end.
func (s *Stack) SelfCheck() error {
	s.reset()
	// Invariant 1 over a randomized mixed sequence, tracked by a parallel
	// naive model of per-scope maps.
	rng := rand.New(rand.NewPCG(1, 2))
	ns := []string{"a", "b", "c", "x", "y", "z"}
	model := []map[string]sym.T{{}}
	naive := func(n string) (sym.T, bool) {
		for i := len(model) - 1; i >= 0; i-- {
			if t, ok := model[i][n]; ok {
				return t, true
			}
		}
		return "", false
	}
	for step := 0; step < 1000; step++ {
		switch rng.IntN(4) {
		case 0:
			if len(model) > 1 && rng.IntN(2) == 0 {
				if err := s.Exit(); err != nil {
					return err
				}
				model = model[:len(model)-1]
			} else {
				s.Enter()
				model = append(model, map[string]sym.T{})
			}
		case 1:
			n, t := ns[rng.IntN(len(ns))], kinds[rng.IntN(2)]
			_, dup := model[len(model)-1][n]
			err := s.Declare(n, t)
			if dup != errors.Is(err, ErrDuplicate) || (!dup && err != nil) {
				return fmt.Errorf("step %d declare %s err=%v dup=%v", step, n, err, dup)
			}
			if !dup {
				model[len(model)-1][n] = t
			}
		default:
			n := ns[rng.IntN(len(ns))]
			got, err := s.Lookup(n)
			want, found := naive(n)
			if found != (err == nil) || found && got != want {
				return fmt.Errorf("step %d lookup %s %s,%v want %s", step, n, got, err, want)
			}
		}
	}
	// Invariants 2-4 on the canonical eight-step scenario.
	s.reset()
	if err := s.Declare("x", sym.Int); err != nil {
		return err
	}
	s.Enter()
	if err := s.Declare("y", sym.Bool); err != nil {
		return err
	}
	if err := s.Declare("x", sym.Bool); err != nil {
		return err
	}
	if t, _ := s.Lookup("x"); t != sym.Bool {
		return errors.New("shadow: inner Lookup x not bool")
	}
	if err := s.Exit(); err != nil {
		return err
	}
	if t, _ := s.Lookup("x"); t != sym.Int {
		return errors.New("shadow: x not restored to int after Exit")
	}
	if _, err := s.Lookup("y"); !errors.Is(err, ErrUndeclared) {
		return fmt.Errorf("y after Exit: want ErrUndeclared, got %v", err)
	}
	if err := s.Declare("z", sym.Int); err != nil {
		return err
	}
	if err := s.Declare("z", sym.Bool); !errors.Is(err, ErrDuplicate) {
		return fmt.Errorf("duplicate z: %v", err)
	}
	if t, _ := s.Lookup("z"); t != sym.Int {
		return errors.New("duplicate: first binding overwritten")
	}
	d := len(s.frames)
	if err := s.Exit(); !errors.Is(err, ErrExitGlobal) || len(s.frames) != d {
		return fmt.Errorf("exit global: err=%v depth=%d", err, len(s.frames))
	}
	s.reset()
	return nil
}
