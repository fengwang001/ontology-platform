package access_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"ontology/access"
	"ontology/reaper"
)

var cfg = access.Config{P: 100, M: 2, Lmax: 300, E: 2, Cool: 50}

func b(s string) []byte { return []byte(s) }

func errName(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, access.ErrClock):
		return "ErrClock"
	case errors.Is(err, access.ErrCooldown):
		return "ErrCooldown"
	case errors.Is(err, access.ErrDuplicatePending):
		return "ErrDuplicatePending"
	case errors.Is(err, access.ErrNotPending):
		return "ErrNotPending"
	case errors.Is(err, access.ErrSelfApprove):
		return "ErrSelfApprove"
	case errors.Is(err, access.ErrNotOwner):
		return "ErrNotOwner"
	case errors.Is(err, access.ErrActiveGrant):
		return "ErrActiveGrant"
	case errors.Is(err, access.ErrLimit):
		return "ErrLimit"
	case errors.Is(err, access.ErrNoGrant):
		return "ErrNoGrant"
	case errors.Is(err, access.ErrDepth):
		return "ErrDepth"
	case errors.Is(err, access.ErrNotRoot):
		return "ErrNotRoot"
	case errors.Is(err, access.ErrExtendLimit):
		return "ErrExtendLimit"
	case errors.Is(err, access.ErrTooLong):
		return "ErrTooLong"
	default:
		return "ErrInvalid"
	}
}

func logNames(log []reaper.Entry) string {
	parts := make([]string, len(log))
	for i, e := range log {
		parts[i] = fmt.Sprintf("g%d:%s@%d", e.GrantID, e.Cause, e.At)
	}
	return strings.Join(parts, ",")
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}

func must2(t *testing.T, id int64, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if id == 0 {
		t.Fatal("zero grant id")
	}
}

// 表驱动：边界、拒绝次序、孙次序、Check 不落地、touched。
func TestTable(t *testing.T) {
	type op struct {
		name string
		fn   func(s *access.System) error
		want string
	}
	tests := []struct {
		name string
		ops  []op
	}{
		{
			"request_expires_exactly_P",
			[]op{
				{"owners", func(s *access.System) error { return s.SetOwners(b("r"), [][]byte{b("o")}, 0) }, "nil"},
				{"req", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 10, 0) }, "nil"},
				{"approve_at_99", func(s *access.System) error { _, err := s.Approve("q", b("o"), 99); return err }, "nil"},
				{"req2_clock_back", func(s *access.System) error { return s.Request("q2", b("z"), b("r"), 1, 98) }, "ErrClock"},
			},
		},
		{
			"request_exactly_P_is_expired",
			[]op{
				{"owners", func(s *access.System) error { return s.SetOwners(b("r"), [][]byte{b("o")}, 0) }, "nil"},
				{"req", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 10, 0) }, "nil"},
				{"approve_at_100", func(s *access.System) error { _, err := s.Approve("q", b("o"), 100); return err }, "ErrNotPending"},
				{"resubmit_ok", func(s *access.System) error { return s.Request("q2", b("u"), b("r"), 10, 100) }, "nil"},
			},
		},
		{
			"request_reject_order",
			[]op{
				{"bad_dur", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 0, 0) }, "ErrInvalid"},
				{"bad_party", func(s *access.System) error { return s.Request("q", nil, b("r"), 1, 0) }, "ErrInvalid"},
				{"bad_time", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 1, -1) }, "ErrInvalid"},
				{"ok", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 1, 5) }, "nil"},
				{"clock_back", func(s *access.System) error { return s.Request("q2", b("u"), b("r"), 1, 4) }, "ErrClock"},
				{"dup_pending", func(s *access.System) error { return s.Request("q3", b("u"), b("r"), 1, 5) }, "ErrDuplicatePending"},
				{"dup_id", func(s *access.System) error { return s.Request("q", b("u2"), b("r2"), 1, 5) }, "ErrInvalid"},
			},
		},
		{
			"approve_order_and_active_limit",
			[]op{
				{"bad_args", func(s *access.System) error { _, err := s.Approve("", b("o"), 0); return err }, "ErrInvalid"},
				{"owners", func(s *access.System) error { return s.SetOwners(b("r"), [][]byte{b("o")}, 0) }, "nil"},
				{"missing", func(s *access.System) error { _, err := s.Approve("nope", b("o"), 0); return err }, "ErrInvalid"},
				{"req", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 10, 0) }, "nil"},
				{"self", func(s *access.System) error { _, err := s.Approve("q", b("u"), 0); return err }, "ErrSelfApprove"},
				{"not_owner", func(s *access.System) error { _, err := s.Approve("q", b("x"), 0); return err }, "ErrNotOwner"},
				{"ok", func(s *access.System) error { _, err := s.Approve("q", b("o"), 0); return err }, "nil"},
				{"req2_dup_active", func(s *access.System) error {
					if err := s.Request("q2", b("u"), b("r"), 10, 1); err != nil {
						return err
					}
					_, err := s.Approve("q2", b("o"), 1)
					return err
				}, "ErrActiveGrant"},
			},
		},
		{
			"limit_M_across_resources",
			[]op{
				{"owners", func(s *access.System) error {
					return s.SetOwners(b("r"), [][]byte{b("o")}, 0)
				}, "nil"},
				{"owners2", func(s *access.System) error { return s.SetOwners(b("r2"), [][]byte{b("o")}, 0) }, "nil"},
				{"owners3", func(s *access.System) error { return s.SetOwners(b("r3"), [][]byte{b("o")}, 0) }, "nil"},
				{"a1", func(s *access.System) error {
					s.Request("a", b("u"), b("r"), 100, 0)
					_, err := s.Approve("a", b("o"), 0)
					return err
				}, "nil"},
				{"a2", func(s *access.System) error {
					s.Request("b", b("u"), b("r2"), 100, 0)
					_, err := s.Approve("b", b("o"), 0)
					return err
				}, "nil"},
				{"a3_limit", func(s *access.System) error {
					s.Request("c", b("u"), b("r3"), 100, 0)
					_, err := s.Approve("c", b("o"), 0)
					return err
				}, "ErrLimit"},
			},
		},
		{
			"extend_from_original_end_and_exact_lmax",
			[]op{
				{"owners", func(s *access.System) error { return s.SetOwners(b("r"), [][]byte{b("o")}, 0) }, "nil"},
				{"req", func(s *access.System) error { return s.Request("q", b("u"), b("r"), 100, 0) }, "nil"},
				{"approve", func(s *access.System) error { _, err := s.Approve("q", b("o"), 10); return err }, "nil"},
				{"ext1", func(s *access.System) error { return s.Extend(1, 50, 50) }, "nil"},
				{"end_160", func(s *access.System) error {
					if n := s.Grant(1); n.End != 160 {
						return fmt.Errorf("end=%d want 160", n.End)
					}
					return nil
				}, "nil"},
				{"ext2_exact", func(s *access.System) error { return s.Extend(1, 150, 60) }, "nil"},
				{"end_310_dur_300", func(s *access.System) error {
					n := s.Grant(1)
					if n.End != 310 || n.End-n.Start != 300 {
						return fmt.Errorf("end=%d dur=%d", n.End, n.End-n.Start)
					}
					return nil
				}, "nil"},
				{"ext3_limit", func(s *access.System) error { return s.Extend(1, 1, 61) }, "ErrExtendLimit"},
				{"extend_child_not_root", func(s *access.System) error {
					_, err := s.Delegate(1, b("v"), 10, 62)
					if err != nil {
						return err
					}
					return s.Extend(2, 1, 62)
				}, "ErrNotRoot"},
			},
		},
		{
			"revoke_order",
			[]op{
				{"owners", func(s *access.System) error { return s.SetOwners(b("r"), [][]byte{b("o")}, 0) }, "nil"},
				{"bad_args", func(s *access.System) error { return s.Revoke(0, b("o"), 0) }, "ErrInvalid"},
				{"no_grant", func(s *access.System) error { return s.Revoke(9, b("o"), 0) }, "ErrNoGrant"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := access.New(cfg)
			for i, o := range tc.ops {
				err := o.fn(s)
				got := errName(err)
				t.Logf("%s: in=%s out=%s basis=%s", tc.name, o.name, got, o.want)
				if got != o.want {
					t.Fatalf("op %d %s: got %s want %s", i, o.name, got, o.want)
				}
			}
		})
	}
}

// 孙紧随其子：父撤销时，先子后孙（孙紧随其子），子按 grantId 升序。
func TestGrandchildOrdering(t *testing.T) {
	s := access.New(cfg)
	must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
	must(t, s.Request("q", b("u"), b("r"), 300, 0))
	root, err := s.Approve("q", b("o"), 0)
	must2(t, root, err)
	c1, err := s.Delegate(root, b("v1"), 299, 1)
	must2(t, c1, err)
	gc1, err := s.Delegate(c1, b("w1"), 298, 2)
	must2(t, gc1, err)
	c2, err := s.Delegate(root, b("v2"), 297, 3)
	must2(t, c2, err)
	gc2, err := s.Delegate(c2, b("w2"), 296, 4)
	must2(t, gc2, err)
	must(t, s.Revoke(root, b("o"), 10))
	got := logNames(s.Reaped())
	want := fmt.Sprintf("g%d:Revoked@10,g%d:Cascaded@10,g%d:Cascaded@10,g%d:Cascaded@10,g%d:Cascaded@10",
		root, c1, gc1, c2, gc2)
	t.Logf("in=revoke(root) out=%s basis=%s", got, want)
	if got != want {
		t.Fatalf("order=%s want=%s", got, want)
	}
}

// Check 只读：不落地回收、不推进时钟；touched ≤ 3。
func TestCheckReadOnlyAndTouched(t *testing.T) {
	s := access.New(cfg)
	must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
	must(t, s.Request("q", b("u"), b("r"), 100, 0))
	root, err := s.Approve("q", b("o"), 0)
	must2(t, root, err)
	child, err := s.Delegate(root, b("v"), 100, 0)
	must2(t, child, err)
	if !s.Check(b("v"), b("r"), 50) {
		t.Fatal("v@50 should be true")
	}
	if s.Touched() != 2 {
		t.Fatalf("touched=%d want 2 (self+root)", s.Touched())
	}
	if s.Check(b("v"), b("r"), 100) {
		t.Fatal("half-open: v@100 should be false (child capped at root end 100)")
	}
}

func TestCheckReadOnly(t *testing.T) {
	s := access.New(cfg)
	must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
	must(t, s.Request("q", b("u"), b("r"), 10, 0))
	root, err := s.Approve("q", b("o"), 0)
	must2(t, root, err)
	if s.Check(b("u"), b("r"), 10) {
		t.Fatal("u@10 should be false (expired)")
	}
	if len(s.Reaped()) != 0 {
		t.Fatalf("check must not land reaping: %v", s.Reaped())
	}
	if s.Touched() > 3 {
		t.Fatalf("touched=%d > 3", s.Touched())
	}
	must(t, s.Tick(10))
	if len(s.Reaped()) != 1 {
		t.Fatalf("after tick reaped=%v", s.Reaped())
	}
}

// 题设示例：公共前缀 + 两个分支分别用独立系统复放。
func TestSpecExample(t *testing.T) {
	setup := func(t *testing.T, s *access.System) (g1, g2, g3 int64) {
		must(t, s.SetOwners(b("r"), [][]byte{b("o")}, 0))
		must(t, s.Request("q1", b("u"), b("r"), 100, 0))
		if _, err := s.Approve("q1", b("u"), 5); !errors.Is(err, access.ErrSelfApprove) {
			t.Fatalf("self approve: %v", err)
		}
		var err error
		g1, err = s.Approve("q1", b("o"), 10)
		must2(t, g1, err)
		g2, err = s.Delegate(g1, b("v"), 500, 20)
		must2(t, g2, err)
		g3, err = s.Delegate(g2, b("w"), 30, 30)
		must2(t, g3, err)
		if _, err := s.Delegate(g3, b("x"), 10, 31); !errors.Is(err, access.ErrDepth) {
			t.Fatalf("depth: %v", err)
		}
		if n := s.Grant(g2); n.End != 110 {
			t.Fatalf("g2 capped end=%d", n.End)
		}
		if n := s.Grant(g3); n.End != 60 {
			t.Fatalf("g3 end=%d", n.End)
		}
		return g1, g2, g3
	}

	t.Run("branch_A_expire", func(t *testing.T) {
		s := access.New(cfg)
		g1, g2, g3 := setup(t, s)
		must(t, s.Tick(110))
		got := logNames(s.Reaped())
		want := fmt.Sprintf("g%d:Expired@60,g%d:Expired@110,g%d:Cascaded@110", g3, g1, g2)
		if got != want {
			t.Fatalf("reaped=%s want=%s", got, want)
		}
	})

	t.Run("branch_B_extend", func(t *testing.T) {
		s := access.New(cfg)
		g1, g2, g3 := setup(t, s)
		must(t, s.Extend(g1, 100, 100))
		if n := s.Grant(g1); n.End != 210 {
			t.Fatalf("end after ext1=%d", n.End)
		}
		if err := s.Extend(g1, 101, 101); !errors.Is(err, access.ErrTooLong) {
			t.Fatalf("too long: %v", err)
		}
		must(t, s.Extend(g1, 100, 101))
		if n := s.Grant(g1); n.End != 310 {
			t.Fatalf("end after ext2=%d", n.End)
		}
		if err := s.Extend(g1, 1, 102); !errors.Is(err, access.ErrExtendLimit) {
			t.Fatalf("ext limit: %v", err)
		}
		if !s.Check(b("v"), b("r"), 109) || s.Check(b("v"), b("r"), 110) {
			t.Fatal("check v 109/110 mismatch")
		}
		if !s.Check(b("u"), b("r"), 110) {
			t.Fatal("check u 110 should be true (extended root)")
		}
		must(t, s.Tick(110))
		got := logNames(s.Reaped())
		want := fmt.Sprintf("g%d:Expired@60,g%d:Expired@110", g3, g2)
		if got != want {
			t.Fatalf("reaped=%s want=%s", got, want)
		}
		must(t, s.Revoke(g1, b("o"), 120))
		if err := s.Request("q2", b("u"), b("r"), 10, 169); !errors.Is(err, access.ErrCooldown) {
			t.Fatalf("cooldown: %v", err)
		}
		must(t, s.Request("q2", b("u"), b("r"), 10, 170))
	})
}
