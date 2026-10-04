package release_test

import (
	"errors"
	"testing"

	"ontology/budget"
	"ontology/probe"
	"ontology/release"
)

type system struct {
	probe   *probe.Store
	release *release.Store
	eval    *budget.Evaluator
}

// Hi=80, Delta=20, W=3, G=10, H=5, Bmax=30, Q=50（规范示例参数）。
func newSystem(t *testing.T) *system {
	t.Helper()
	clock := &probe.Clock{}
	ps, err := probe.NewStore(clock, probe.Config{Hi: 80, Delta: 20, W: 3, G: 10})
	if err != nil {
		t.Fatal(err)
	}
	rs, err := release.NewStore(clock, ps)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := budget.NewEvaluator(clock, ps, rs, budget.Config{H: 5, Bmax: 30, Q: 50})
	if err != nil {
		t.Fatal(err)
	}
	return &system{probe: ps, release: rs, eval: ev}
}

func (s *system) mustReading(t *testing.T, dev string, temp, now int64) {
	t.Helper()
	if err := s.probe.Reading(dev, temp, now); err != nil {
		t.Fatalf("Reading(%s,%d,%d): %v", dev, temp, now, err)
	}
}

func TestRegisterLoadUnloadRejections(t *testing.T) {
	cases := []struct {
		name string
		run  func(s *system) error
		want error
	}{
		{"Register空标识", func(s *system) error { return s.release.Register("", "D", 0) }, probe.ErrInvalidParam},
		{"Register时钟回退", func(s *system) error {
			s.mustReading(t, "D", 50, 10)
			return s.release.Register("U", "D", 5)
		}, probe.ErrClockBack},
		{"Register设备不存在", func(s *system) error { return s.release.Register("U", "D", 0) }, probe.ErrNotFound},
		{"Register单元冲突", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			return s.release.Register("U", "D", 2)
		}, probe.ErrConflict},
		{"Load单元不存在", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			return s.release.Load("U", "D", 1)
		}, probe.ErrNotFound},
		{"Load设备不存在", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			if err := s.release.Unload("U", "D", 2); err != nil {
				t.Fatal(err)
			}
			return s.release.Load("U", "D2", 3)
		}, probe.ErrNotFound},
		{"Load时已在设备上", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			return s.release.Load("U", "D", 2)
		}, probe.ErrState},
		{"Unload不在该设备上", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			s.mustReading(t, "D2", 50, 1)
			if err := s.release.Register("U", "D", 2); err != nil {
				t.Fatal(err)
			}
			return s.release.Unload("U", "D2", 3)
		}, probe.ErrState},
		{"Unload未装载", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			if err := s.release.Unload("U", "D", 2); err != nil {
				t.Fatal(err)
			}
			return s.release.Unload("U", "D", 3)
		}, probe.ErrState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(newSystem(t)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReleasePermissionOrder(t *testing.T) {
	qa := release.Operator{Name: "qa", Perms: release.PermRelease | release.PermQA}
	noPerm := release.Operator{Name: "x"}
	cases := []struct {
		name string
		run  func(s *system) error
		want error
	}{
		{"无Release权限优先于单元不存在", func(s *system) error {
			return s.release.Release("U", noPerm, 0)
		}, probe.ErrNoReleasePerm},
		{"时钟回退优先于无Release权限", func(s *system) error {
			s.mustReading(t, "D", 50, 10)
			return s.release.Release("U", noPerm, 5)
		}, probe.ErrClockBack},
		{"单元不存在", func(s *system) error {
			return s.release.Release("U", qa, 0)
		}, probe.ErrNotFound},
		{"重复放行报状态不符", func(s *system) error {
			s.mustReading(t, "D", 50, 0)
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			if err := s.release.Release("U", qa, 2); err != nil {
				t.Fatal(err)
			}
			return s.release.Release("U", qa, 3)
		}, probe.ErrState},
		{"已判废拒绝", func(s *system) error {
			s.mustReading(t, "D", 200, 0) // 重度，每分钟 3
			if err := s.release.Register("U", "D", 1); err != nil {
				t.Fatal(err)
			}
			return s.release.Release("U", qa, 20) // E=57 > 30
		}, probe.ErrSpoiled},
		{"缺QA复核权限", func(s *system) error {
			s.mustReading(t, "D", 90, 0) // 轻度，每分钟 1
			if err := s.release.Register("U", "D", 0); err != nil {
				t.Fatal(err)
			}
			// t=15: E=15, 15*100=1500 >= 30*50=1500，恰等也需 QA
			return s.release.Release("U", release.Operator{Name: "op", Perms: release.PermRelease}, 15)
		}, probe.ErrNoQAPerm},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.run(newSystem(t)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// 放行后单元进入终态：Load、Unload、Release 均报状态不符。
func TestReleasedUnitIsTerminal(t *testing.T) {
	s := newSystem(t)
	s.mustReading(t, "D", 50, 0)
	if err := s.release.Register("U", "D", 1); err != nil {
		t.Fatal(err)
	}
	qa := release.Operator{Name: "qa", Perms: release.PermRelease | release.PermQA}
	if err := s.release.Release("U", qa, 2); err != nil {
		t.Fatal(err)
	}
	if err := s.release.Load("U", "D", 3); !errors.Is(err, probe.ErrState) {
		t.Fatalf("Load after release: %v", err)
	}
	if err := s.release.Unload("U", "D", 3); !errors.Is(err, probe.ErrState) {
		t.Fatalf("Unload after release: %v", err)
	}
	if err := s.release.Release("U", qa, 3); !errors.Is(err, probe.ErrState) {
		t.Fatalf("Release after release: %v", err)
	}
}

// 判废单元仍可 Load 与 Unload。
func TestSpoiledUnitCanStillMove(t *testing.T) {
	s := newSystem(t)
	s.mustReading(t, "D", 200, 0)
	if err := s.release.Register("U", "D", 0); err != nil {
		t.Fatal(err)
	}
	res, err := s.eval.Evaluate("U", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Spoiled {
		t.Fatalf("want spoiled, got %+v", res)
	}
	if err := s.release.Unload("U", "D", 21); err != nil {
		t.Fatalf("Unload spoiled: %v", err)
	}
	if err := s.release.Load("U", "D", 22); err != nil {
		t.Fatalf("Load spoiled: %v", err)
	}
}
