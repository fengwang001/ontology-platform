package source

import (
	"errors"
	"strings"
	"testing"
)

func TestFromPairsAndArgs(t *testing.T) {
	cases := []struct {
		name    string
		pairs   map[string]string
		args    []string
		wantErr error
		anyErr  bool
	}{
		{"pairs ok", map[string]string{"a.b": "1", "a.c": ""}, nil, nil, false},
		{"empty key", map[string]string{"": "1"}, nil, ErrEmptyKey, false},
		{"empty segment", map[string]string{"a..b": "1"}, nil, ErrEmptySegment, false},
		{"deep key", map[string]string{strings.Repeat("s.", 99) + "s": "v"}, nil, nil, false},
		{"args ok", nil, []string{"--x.y=2", "--z="}, nil, false},
		{"args no prefix", nil, []string{"x=1"}, nil, true},
		{"args no equals", nil, []string{"--x"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.pairs != nil {
				_, err = FromPairs(Default, tc.pairs)
			} else {
				_, err = ParseArgs(CLI, tc.args)
			}
			if tc.wantErr == nil && !tc.anyErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if tc.anyErr && err == nil {
				t.Fatal("want an error, got nil")
			}
		})
	}
}

func TestParseFile(t *testing.T) {
	cases := []struct {
		name    string
		data    string
		want    []Entry
		wantErr error
	}{
		{"valid", "a = 1\nb.c = x=y\n.\n", []Entry{
			{Key: "a", Value: "1", Layer: File},
			{Key: "b.c", Value: "x=y", Layer: File},
		}, nil},
		{"empty value", "a =\n.\n", []Entry{{Key: "a", Value: "", Layer: File}}, nil},
		{"empty key", " = 1\n.\n", nil, ErrEmptyKey},
		{"empty segment", "a..b = 1\n.\n", nil, ErrEmptySegment},
		{"no terminator", "a = 1\n", nil, ErrLineIncomplete},
		{"deep key", strings.Repeat("s.", 99) + "s = v\n.\n", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFile(File, []byte(tc.data))
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != nil && !equalEntries(got, tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseFileTruncate(t *testing.T) {
	full := "name = file\ngreeting = hello ${name}\n.\n"
	seen := map[error]int{}
	for i := 1; i < len(full); i++ {
		_, err := ParseFile(File, []byte(full[:i]))
		if err == nil {
			t.Fatalf("truncation at %d produced no error", i)
		}
		matched := false
		for _, class := range []error{ErrLineIncomplete, ErrKeyIncomplete, ErrValueIncomplete} {
			if errors.Is(err, class) {
				seen[class]++
				matched = true
			}
		}
		if !matched {
			t.Fatalf("truncation at %d unclassified: %v", i, err)
		}
		if !strings.Contains(err.Error(), "line ") {
			t.Fatalf("truncation at %d lacks line number: %v", i, err)
		}
	}
	for _, class := range []error{ErrLineIncomplete, ErrKeyIncomplete, ErrValueIncomplete} {
		if seen[class] == 0 {
			t.Fatalf("class %v never observed", class)
		}
	}
}

func TestFromEnv(t *testing.T) {
	cases := []struct {
		name    string
		env     []string
		prefix  string
		wantKey string
		wantErr error
	}{
		{"map dots", []string{"A_B_C=1"}, "", "a.b.c", nil},
		{"prefix filter", []string{"APP_X=1", "OTHER=2"}, "APP_", "x", nil},
		{"duplicate same name", []string{"A_B=1", "A_B=2"}, "", "a.b", nil},
		{"ambiguity", []string{"A_B_C=1", "A__B_C=2"}, "", "", ErrAmbiguousEnv},
		{"bad pair", []string{"NOEQUALS"}, "", "", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FromEnv(Env, tc.env, tc.prefix)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("want %v, got %v", tc.wantErr, err)
				}
				return
			}
			if tc.name == "bad pair" {
				if err == nil {
					t.Fatal("want error for malformed pair")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) == 0 || got[0].Key != tc.wantKey {
				t.Fatalf("got %+v, want key %q", got, tc.wantKey)
			}
		})
	}
}

func equalEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
