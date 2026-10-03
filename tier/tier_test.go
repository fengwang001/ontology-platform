package tier

import (
	"errors"
	"testing"
)

func TestAdd(t *testing.T) {
	cases := []struct {
		name     string
		tierName string
		rank     int
		tt, bb   int64
		wantErr  error
	}{
		{"ok", "free", 1, 1000, 2, nil},
		{"empty name", "", 2, 1000, 2, ErrInvalidArgument},
		{"rank zero", "pro", 0, 1000, 2, ErrInvalidArgument},
		{"rank too big", "pro", 1001, 1000, 2, ErrInvalidArgument},
		{"T zero", "pro", 2, 0, 2, ErrInvalidArgument},
		{"T too big", "pro", 2, 1_000_001, 2, ErrInvalidArgument},
		{"B zero", "pro", 2, 1000, 0, ErrInvalidArgument},
		{"B too big", "pro", 2, 1000, 1_000_001, ErrInvalidArgument},
		{"dup name", "free", 3, 100, 5, ErrDuplicate},
		{"dup rank", "pro", 1, 100, 5, ErrDuplicate},
		{"ok after dups", "pro", 2, 100, 5, nil},
	}
	r := NewRegistry()
	for _, c := range cases {
		err := r.Add(c.tierName, c.rank, c.tt, c.bb)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: Add(%q,%d) err=%v, want %v", c.name, c.tierName, c.rank, err, c.wantErr)
		}
	}
}

func TestDerive(t *testing.T) {
	newReg := func(t *testing.T) *Registry {
		t.Helper()
		r := NewRegistry()
		// 故意先登记高 rank，验证最小 rank 选择不依赖插入顺序。
		for _, args := range []struct {
			name string
			rank int
			tt   int64
			bb   int64
		}{
			{"pro", 2, 100, 5},
			{"free", 1, 1000, 2},
			{"ent", 3, 10, 9},
		} {
			if err := r.Add(args.name, args.rank, args.tt, args.bb); err != nil {
				t.Fatal(err)
			}
		}
		return r
	}
	cases := []struct {
		name    string
		scopes  []string
		want    string
		wantErr error
	}{
		{"single tier scope", []string{"tier:pro"}, "pro", nil},
		{"max rank wins", []string{"tier:free", "tier:ent", "tier:pro"}, "ent", nil},
		{"unregistered ignored", []string{"tier:platinum"}, "free", nil},
		{"unregistered mixed with registered", []string{"tier:platinum", "tier:pro"}, "pro", nil},
		{"no tier scope falls back to min rank", []string{"orders"}, "free", nil},
		{"empty scopes falls back to min rank", nil, "free", nil},
		{"duplicate scopes have no effect", []string{"tier:pro", "tier:pro"}, "pro", nil},
		{"non-prefixed scopes ignored", []string{"tier", "tier:", "xtier:pro"}, "free", nil},
	}
	for _, c := range cases {
		tr, err := newReg(t).Derive(c.scopes)
		if !errors.Is(err, c.wantErr) {
			t.Errorf("%s: err=%v, want %v", c.name, err, c.wantErr)
			continue
		}
		if c.wantErr == nil && tr.Name != c.want {
			t.Errorf("%s: tier=%q, want %q", c.name, tr.Name, c.want)
		}
	}
	if _, err := NewRegistry().Derive([]string{"tier:free"}); !errors.Is(err, ErrNoTiers) {
		t.Errorf("empty registry: err=%v, want %v", err, ErrNoTiers)
	}
}
