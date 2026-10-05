package attest

import (
	"errors"
	"fmt"
	"testing"

	"ontology/registry"
)

func newSystem(t *testing.T) (*registry.Repo, *Store) {
	t.Helper()
	r, err := registry.New([]registry.Stage{
		{Name: "dev"},
		{Name: "prod", Immutable: true, S: 10, Required: []string{"test"}, Trusted: []string{"ci"}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, New(r)
}

func TestAttestValidation(t *testing.T) {
	cases := []struct {
		name                string
		now, exp            int64
		digest, typ, signer string
		want                error
	}{
		{"ok", 0, 1, "d1", "test", "ci", nil},
		{"exp-eq-now", 5, 5, "d1", "test", "ci", registry.ErrInvalidArgument},
		{"exp-lt-now", 5, 4, "d1", "test", "ci", registry.ErrInvalidArgument},
		{"empty-digest", 0, 1, "", "test", "ci", registry.ErrInvalidArgument},
		{"empty-type", 0, 1, "d1", "", "ci", registry.ErrInvalidArgument},
		{"empty-signer", 0, 1, "d1", "test", "", registry.ErrInvalidArgument},
		{"now-negative", -1, 1, "d1", "test", "ci", registry.ErrInvalidArgument},
		{"now-too-big", registry.MaxNow + 1, registry.MaxNow + 2, "d1", "test", "ci", registry.ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, s := newSystem(t)
			err := s.Attest(tc.now, tc.digest, tc.typ, tc.signer, tc.exp)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAttestClockAndRevoke(t *testing.T) {
	r, s := newSystem(t)
	if err := s.Attest(10, "d1", "test", "ci", 100); err != nil {
		t.Fatal(err)
	}
	if err := s.Attest(9, "d1", "test", "ci", 100); !errors.Is(err, registry.ErrClockRewind) {
		t.Fatalf("attest rewind: got %v", err)
	}
	if err := s.RevokeSigner(9, "ci"); !errors.Is(err, registry.ErrClockRewind) {
		t.Fatalf("revoke rewind: got %v", err)
	}
	// 被拒操作不推进时钟：now=10 仍应成功。
	if err := s.RevokeSigner(10, "ci"); err != nil {
		t.Fatalf("revoke at equal now: %v", err)
	}
	r.Lock()
	valid := s.ValidTypesLocked(11, "d1", map[string]bool{"ci": true})
	r.Unlock()
	if len(valid) != 0 {
		t.Fatalf("revoked signer still valid: %v", valid)
	}
}

func TestValidTypesLocked(t *testing.T) {
	r, s := newSystem(t)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Attest(0, "d1", "test", "ci", 100))
	must(s.Attest(0, "d1", "scan", "sec", 50))
	must(s.Attest(0, "d1", "lint", "ops", 1000))
	must(s.Attest(0, "d2", "test", "ci", 100))

	trusted := map[string]bool{"ci": true, "sec": true, "ops": true}
	check := func(tNow int64, digest string, trusted map[string]bool, want map[string]bool) {
		t.Helper()
		r.Lock()
		got := s.ValidTypesLocked(tNow, digest, trusted)
		r.Unlock()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("t=%d digest=%s: got %v, want %v", tNow, digest, got, want)
		}
	}
	check(0, "d1", trusted, map[string]bool{"test": true, "scan": true, "lint": true})
	check(99, "d1", trusted, map[string]bool{"test": true, "lint": true}) // scan(exp=50) 已失效
	// t==exp 恰到期：test(exp=100) 与 scan(exp=50) 均失效。
	check(100, "d1", trusted, map[string]bool{"lint": true})
	check(50, "d1", trusted, map[string]bool{"test": true, "lint": true})
	// 签名者不在 trusted。
	check(0, "d1", map[string]bool{"ci": true, "sec": true}, map[string]bool{"test": true, "scan": true})
	check(0, "d1", map[string]bool{"nobody": true}, map[string]bool{})
	// 证明绑定摘要：d2 的证明不影响 d1。
	check(0, "d2", trusted, map[string]bool{"test": true})
	check(0, "d3", trusted, map[string]bool{})
}

// TestScanIndependentOfRepoSize 证明一次闸门判定检视的证明条数
// 不超过该摘要自身的证明条数，与库内摘要总数无关。
func TestScanIndependentOfRepoSize(t *testing.T) {
	for _, total := range []int{100, 10000} {
		t.Run(fmt.Sprintf("digests=%d", total), func(t *testing.T) {
			r, s := newSystem(t)
			for i := 0; i < total; i++ {
				d := fmt.Sprintf("d%d", i)
				if err := s.Attest(0, d, "test", "ci", 100); err != nil {
					t.Fatal(err)
				}
				if err := s.Attest(0, d, "scan", "ci", 100); err != nil {
					t.Fatal(err)
				}
			}
			s.scanned = 0
			r.Lock()
			s.ValidTypesLocked(50, "d0", map[string]bool{"ci": true})
			r.Unlock()
			if s.scanned != 2 {
				t.Fatalf("total=%d: scanned %d, want 2 (own attestations only)", total, s.scanned)
			}
		})
	}
}
