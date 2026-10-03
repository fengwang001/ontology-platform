package rule

import (
	"errors"
	"testing"
)

func TestNewSetValidation(t *testing.T) {
	cases := []struct {
		name  string
		rules []Rule
	}{
		{"empty id", []Rule{{ID: "", Days: 1}}},
		{"dup id", []Rule{{ID: "x", Days: 1}, {ID: "x", Kind: OrphanMarker, Days: 1}}},
		{"days zero", []Rule{{ID: "x", Days: 0}}},
		{"days too large", []Rule{{ID: "x", Days: 3651}}},
		{"keep negative", []Rule{{ID: "x", Days: 1, Keep: -1}}},
		{"keep too large", []Rule{{ID: "x", Days: 1, Keep: 1001}}},
		{"bad kind", []Rule{{ID: "x", Kind: Kind(9), Days: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewSet(tc.rules...); !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want ErrInvalid", err)
			}
		})
	}
	if _, err := NewSet(
		Rule{ID: "a", Days: 1, Keep: 0},
		Rule{ID: "b", Kind: OrphanMarker, Days: 3650, Keep: 1000},
	); err != nil {
		t.Fatalf("boundary values must be accepted: %v", err)
	}
}

func TestMatchPrecedence(t *testing.T) {
	s, err := NewSet(
		Rule{ID: "b", Prefix: "", Kind: Expire, Days: 5},
		Rule{ID: "a", Prefix: "", Kind: Expire, Days: 1},
		Rule{ID: "c", Prefix: "app", Kind: Expire, Days: 10},
		Rule{ID: "d", Prefix: "app", Kind: NoncurrentExpire, Days: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		key  string
		kind Kind
		want string
		ok   bool
	}{
		{"apple", Expire, "c", true},  // 最长前缀优先
		{"banana", Expire, "a", true}, // 并列取 id 最小
		{"apple", NoncurrentExpire, "d", true},
		{"apple", OrphanMarker, "", false},
	}
	for _, tc := range cases {
		got, ok := s.Match(tc.key, tc.kind)
		if ok != tc.ok || (ok && got.ID != tc.want) {
			t.Fatalf("Match(%q, %v) = (%v, %v), want (%q, %v)",
				tc.key, tc.kind, got.ID, ok, tc.want, tc.ok)
		}
	}
}
