package cycle

import (
	"errors"
	"strconv"
	"testing"
)

func TestTempName(t *testing.T) {
	cases := []struct {
		name     string
		existing []string
		targets  []string
		wantErr  error
		wantPref string
	}{
		{"free", []string{"a", "b"}, []string{"c"}, nil, prefixes[0] + "0"},
		{
			"first family occupied fallback works",
			occupy(prefixes[0], TriesPerFamily), []string{"z"}, nil,
			prefixes[1] + "0",
		},
		{
			"target names also reserved",
			[]string{"a"}, []string{prefixes[0] + "0", prefixes[0] + "1"},
			nil, prefixes[0] + "2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			set := BuildNameSet(tc.existing, tc.targets)
			got, err := TempName(func(c string) bool { _, ok := set[c]; return ok })
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if err == nil && got != tc.wantPref {
				t.Fatalf("got %q want %q", got, tc.wantPref)
			}
		})
	}
}

func TestTempNameExhausted(t *testing.T) {
	blocked := make(map[string]struct{}, 2*TriesPerFamily)
	for _, prefix := range prefixes {
		for i := 0; i < TriesPerFamily; i++ {
			blocked[prefix+strconv.Itoa(i)] = struct{}{}
		}
	}
	_, err := TempName(func(c string) bool { _, ok := blocked[c]; return ok })
	if !errors.Is(err, ErrNoTempName) {
		t.Fatalf("err=%v want ErrNoTempName", err)
	}
}

func occupy(prefix string, n int) []string {
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, prefix+strconv.Itoa(i))
	}
	return out
}
