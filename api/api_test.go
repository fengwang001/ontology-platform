package api_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/api"
)

func snapshotOf(a *api.API) (int, map[string]string) {
	h := a.Snapshot()
	m := map[string]string{}
	for _, k := range []string{"a", "b", "c", "d", "e", "x", ""} {
		if v, ok := h.Read(k); ok {
			m[k] = v
		}
	}
	return h.ID(), m
}

// TestRejectedUpdateLeavesStateUnchanged pins invariant 4: each of the
// four distinct rejection paths publishes nothing and the table stays
// fully usable afterwards.
func TestRejectedUpdateLeavesStateUnchanged(t *testing.T) {
	cases := []struct {
		name    string
		maxLen  int
		maxKeys int
		seed    func(a *api.API)
		k, v    string
		wantErr error
	}{
		{"empty key", 8, 4, func(a *api.API) {}, "", "x", api.ErrEmptyKey},
		{"empty value", 8, 4, func(a *api.API) {}, "x", "", api.ErrEmptyValue},
		{"key too long", 8, 4, func(a *api.API) {}, "toolongkey", "x", api.ErrKeyTooLong},
		{"too many keys", 8, 4, func(a *api.API) {
			_ = a.Update("a", "1")
			_ = a.Update("b", "2")
			_ = a.Update("c", "3")
			_ = a.Update("d", "4")
		}, "e", "5", api.ErrTooManyKeys},
	}
	seen := map[error]bool{}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := api.New(tc.maxLen, tc.maxKeys)
			tc.seed(a)
			beforeID, beforeM := snapshotOf(a)
			err := a.Update(tc.k, tc.v)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("case %d: err %v, want %v", i, err, tc.wantErr)
			}
			seen[tc.wantErr] = true
			afterID, afterM := snapshotOf(a)
			if afterID != beforeID || fmt.Sprint(afterM) != fmt.Sprint(beforeM) {
				t.Fatalf("state changed on reject: id %d->%d m %v->%v",
					beforeID, afterID, beforeM, afterM)
			}
			if e := a.Update("a", "9"); e != nil {
				t.Fatalf("table unusable after rejection: %v", e)
			}
		})
	}
	if len(seen) != 4 {
		t.Fatalf("the four rejection errors are not all distinct: %d seen", len(seen))
	}
}

// TestUpdateExistingKeyAtCapacityDoesNotCount ensures overwriting an
// existing key at the cap is allowed (distinct count does not grow).
func TestUpdateExistingKeyAtCapacityDoesNotCount(t *testing.T) {
	a := api.New(8, 2)
	if err := a.Update("a", "1"); err != nil {
		t.Fatal(err)
	}
	if err := a.Update("b", "2"); err != nil {
		t.Fatal(err)
	}
	if err := a.Update("a", "9"); err != nil {
		t.Fatalf("overwrite at capacity rejected: %v", err)
	}
	if v, _ := a.Read("a"); v != "9" {
		t.Fatalf("a = %q, want 9", v)
	}
}

// TestSelfCheck runs the built-in verification of all four invariants.
func TestSelfCheck(t *testing.T) {
	if err := api.New(64, 1000).SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}
