// Package api 是对外门面：组合 obx 与 rly，提供 SelfCheck 自检。依赖 rly。
package api

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"

	"ontology/obx"
	"ontology/rly"
)

type Service struct {
	box   *obx.Box
	relay *rly.Relay
}

func New(maxPending int) *Service {
	b := obx.New(maxPending)
	return &Service{box: b, relay: rly.New(b)}
}

func (s *Service) Write(tx, payload string) error { return s.box.Write(tx, payload) }
func (s *Service) Commit(tx string) error         { return s.box.Commit(tx) }
func (s *Service) Abort(tx string) error          { return s.box.Abort(tx) }

func (s *Service) Relay(crash bool) []int { return s.relay.Relay(crash) }

func (s *Service) Applied() []int { return s.relay.Applied() }
func (s *Service) Dups() int      { return s.relay.Dups() }

func (s *Service) SelfCheck() error {
	for _, check := range []func() error{checkTenSteps, checkNaive, checkRejections} {
		if err := check(); err != nil {
			return err
		}
	}
	return nil
}

func checkTenSteps() error {
	a := New(16)
	a.Write("T1", "p")
	a.Write("T2", "p")
	a.Commit("T2")
	d4 := a.Relay(false)
	a.Write("T3", "p")
	a.Write("T1", "p")
	a.Commit("T3")
	a.Commit("T1")
	d9 := a.Relay(true)
	d10 := a.Relay(false)
	if !slices.Equal(d4, []int{2}) || !slices.Equal(d9, []int{3, 1, 4}) || !slices.Equal(d10, []int{4}) {
		return fmt.Errorf("ten-step deliveries %v %v %v", d4, d9, d10)
	}
	if !slices.Equal(a.Applied(), []int{2, 3, 1, 4}) || a.Dups() != 1 {
		return fmt.Errorf("ten-step final: applied=%v dups=%d", a.Applied(), a.Dups())
	}
	return nil
}

func checkNaive() error {
	for seed := int64(1); seed <= 30; seed++ {
		rng := rand.New(rand.NewSource(seed))
		a, want := New(64), []int{}
		open, closed, nextID := map[string][]int{}, map[string]bool{}, 0
		names := []string{"a", "b", "c", "d"}
		for i := 0; i < 60; i++ {
			n := names[rng.Intn(len(names))]
			switch rng.Intn(4) {
			case 0, 1:
				if closed[n] {
					continue
				}
				if err := a.Write(n, "p"); err != nil {
					return err
				}
				nextID++
				open[n] = append(open[n], nextID)
			case 2:
				if closed[n] || len(open[n]) == 0 {
					continue
				}
				if rng.Intn(2) == 0 {
					if err := a.Commit(n); err != nil {
						if !errors.Is(err, obx.ErrBacklog) {
							return err
						}
						continue // 积压超限被拒：两边都保持开启
					}
					want = append(want, open[n]...)
				} else if err := a.Abort(n); err != nil {
					return err
				}
				closed[n] = true
				delete(open, n)
			case 3:
				a.Relay(rng.Intn(3) == 0)
			}
		}
		a.Relay(false)
		if got := a.Applied(); !slices.Equal(got, want) {
			return fmt.Errorf("seed %d: got %v want %v", seed, got, want)
		}
	}
	return nil
}

func checkRejections() error {
	a := New(2)
	rej := func(err, sentinel error, what string) error {
		if !errors.Is(err, sentinel) {
			return fmt.Errorf("%s: want %v, got %v", what, sentinel, err)
		}
		return nil
	}
	if err := rej(a.Write("x", ""), obx.ErrEmptyPayload, "empty payload"); err != nil {
		return err
	}
	if err := rej(a.Commit("ghost"), obx.ErrTxUnavailable, "ghost commit"); err != nil {
		return err
	}
	if err := rej(a.Abort("ghost"), obx.ErrTxUnavailable, "ghost abort"); err != nil {
		return err
	}
	a.Write("x", "p")
	a.Write("x", "p")
	if err := a.Commit("x"); err != nil {
		return err
	}
	a.Write("y", "p")
	if err := rej(a.Commit("y"), obx.ErrBacklog, "backlog"); err != nil {
		return err
	}
	a.Write("y", "p") // 被拒后 y 仍开启，可继续写入
	if err := rej(a.Commit("y"), obx.ErrBacklog, "backlog twice"); err != nil {
		return err
	}
	a.Relay(false) // 腾出积压空间
	if err := a.Commit("y"); err != nil {
		return err
	}
	a.Relay(false)
	if !slices.Equal(a.Applied(), []int{1, 2, 3, 4}) || a.Dups() != 0 {
		return fmt.Errorf("state changed by rejections: %v", a.Applied())
	}
	return rej(a.Write("x", "p"), obx.ErrTxUnavailable, "committed tx reuse")
}
