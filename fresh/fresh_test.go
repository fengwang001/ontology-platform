package fresh_test

import (
	"errors"
	"strings"
	"testing"

	"ontology/dag"
	"ontology/fresh"
)

func newStore(t *testing.T) *fresh.Store {
	t.Helper()
	g, err := dag.New(100)
	if err != nil {
		t.Fatal(err)
	}
	for _, ds := range []struct {
		name    string
		off     int64
		dur     int64
		parents []string
	}{
		{"pa", 10, 0, nil},
		{"pb", 10, 0, nil},
		{"q", 50, 5, []string{"pb", "pa"}},
	} {
		if err := g.AddDataset(ds.name, ds.off, ds.dur, ds.parents); err != nil {
			t.Fatal(err)
		}
	}
	return fresh.New(g)
}

func TestLandRejectOrder(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(s *fresh.Store)
		d       string
		k       int64
		now     int64
		want    error
		wantMsg string
	}{
		{"already", func(s *fresh.Store) {
			if err := s.Land("pa", 0, 10); err != nil {
				t.Fatal(err)
			}
		}, "pa", 0, 20, fresh.ErrAlready, ""},
		{"out of order beats too early", nil, "pa", 5, 10, fresh.ErrOutOfOrder, ""},
		{"too early beats upstream missing", func(s *fresh.Store) {
			if err := s.Land("q", 0, 0); err == nil {
				t.Fatal("expect upstream missing for q period 0")
			}
			// 让 q 的第 0 期落地：先补父。
			if err := s.Land("pa", 0, 10); err != nil {
				t.Fatal(err)
			}
			if err := s.Land("pb", 0, 10); err != nil {
				t.Fatal(err)
			}
			if err := s.Land("q", 0, 20); err != nil {
				t.Fatal(err)
			}
		}, "q", 1, 50, fresh.ErrTooEarly, ""}, // k*T=100 > 50，且父第 1 期未落地
		{"upstream missing smallest name", nil, "q", 0, 10, fresh.ErrUpstreamMissing, "pa"},
		{"ok", func(s *fresh.Store) {
			if err := s.Land("pa", 0, 10); err != nil {
				t.Fatal(err)
			}
			if err := s.Land("pb", 0, 10); err != nil {
				t.Fatal(err)
			}
		}, "q", 0, 60, nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if tc.prepare != nil {
				tc.prepare(s)
			}
			err := s.Land(tc.d, tc.k, tc.now)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if tc.wantMsg != "" && !strings.Contains(err.Error(), tc.wantMsg) {
				t.Fatalf("error %q should mention %q", err, tc.wantMsg)
			}
		})
	}
}

func TestViolationBoundaries(t *testing.T) {
	s := newStore(t)
	// 未落地：t == 截止 不算违约，t > 截止 才算。
	if s.Violation("pa", 0, 10) {
		t.Error("unlanded at t==deadline must not violate")
	}
	if !s.Violation("pa", 0, 11) {
		t.Error("unlanded at t>deadline must violate")
	}
	// 落地恰等截止为准时。
	if err := s.Land("pa", 0, 10); err != nil {
		t.Fatal(err)
	}
	if s.Violation("pa", 0, 1_000_000) {
		t.Error("landed exactly at deadline must not violate")
	}
	// 晚 1 秒落地即违约。
	if err := s.Land("pb", 0, 11); err != nil {
		t.Fatal(err)
	}
	if !s.Violation("pb", 0, 11) {
		t.Error("landed after deadline must violate")
	}
}

func TestOpenPeriodSkipping(t *testing.T) {
	s := newStore(t)
	if got := s.FirstOpen("pa"); got != 0 {
		t.Fatalf("FirstOpen=%d, want 0", got)
	}
	// 准时落地即结案。
	for k := int64(0); k < 5; k++ {
		if err := s.Land("pa", k, k*100+10); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.FirstOpen("pa"); got != 5 {
		t.Fatalf("FirstOpen=%d, want 5", got)
	}
	// 迟落地不结案，告警结案后跳过；乱序结案也能跳过。
	if err := s.Land("pa", 5, 1000); err != nil { // 迟于截止 510
		t.Fatal(err)
	}
	if err := s.Land("pa", 6, 610); err != nil { // 准时
		t.Fatal(err)
	}
	if got := s.FirstOpen("pa"); got != 5 {
		t.Fatalf("FirstOpen=%d, want 5 (late landing stays open)", got)
	}
	s.Close("pa", 5)
	if got := s.FirstOpen("pa"); got != 7 {
		t.Fatalf("FirstOpen=%d, want 7", got)
	}
	if got := s.NextOpen("pa", 5); got != 7 {
		t.Fatalf("NextOpen=%d, want 7", got)
	}
}
