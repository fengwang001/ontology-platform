package tier_test

import (
	"errors"
	"testing"

	"ontology/tier"
)

func TestRegistryAddAndResolve(t *testing.T) {
	r := tier.NewRegistry()
	if _, err := r.Resolve(nil); !errors.Is(err, tier.ErrNoTier) {
		t.Fatalf("empty registry: %v", err)
	}
	cases := []struct {
		name    string
		rank    int64
		tt, b   int64
		wantErr error
	}{
		{"", 1, 1, 1, tier.ErrInvalidArg},
		{"free", 0, 1, 1, tier.ErrInvalidArg},
		{"free", 1001, 1, 1, tier.ErrInvalidArg},
		{"free", 1, 0, 1, tier.ErrInvalidArg},
		{"free", 1, 1_000_001, 1, tier.ErrInvalidArg},
		{"free", 1, 1, 0, tier.ErrInvalidArg},
		{"free", 1, 1000, 2, nil},
		{"dup", 1, 1, 1, tier.ErrDuplicate}, // duplicate rank
		{"free", 9, 1, 1, tier.ErrDuplicate},
		{"pro", 2, 100, 5, nil},
	}
	for _, c := range cases {
		if got := r.Add(c.name, c.rank, c.tt, c.b); !errors.Is(got, c.wantErr) {
			t.Fatalf("Add(%q,%d): got %v want %v", c.name, c.rank, got, c.wantErr)
		}
	}

	resolve := []struct {
		scopes []string
		want   string
	}{
		{nil, "free"},                       // no mark -> lowest rank
		{[]string{"orders"}, "free"},        // plain scopes ignored for tier
		{[]string{"tier:platinum"}, "free"}, // unknown tier ignored
		{[]string{"tier:pro"}, "pro"},
		{[]string{"tier:free", "tier:pro", "tier:platinum"}, "pro"}, // max rank
	}
	for _, c := range resolve {
		tr, err := r.Resolve(c.scopes)
		if err != nil {
			t.Fatalf("Resolve(%v): %v", c.scopes, err)
		}
		if tr.Name != c.want {
			t.Fatalf("Resolve(%v) = %s, want %s", c.scopes, tr.Name, c.want)
		}
	}
}
