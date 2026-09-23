package source

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeKey(t *testing.T) {
	deep := strings.Repeat("a.", 99) + "z" // 深度 100
	cases := []struct {
		name    string
		key     string
		want    string
		wantErr error
	}{
		{"simple", "a.b.c", "a.b.c", nil},
		{"trim spaces", "  a.b  ", "a.b", nil},
		{"depth 100", deep, deep, nil},
		{"empty", "", "", ErrEmptyKey},
		{"spaces only", "   ", "", ErrEmptyKey},
		{"empty segment mid", "a..b", "", ErrEmptySegment},
		{"empty segment head", ".a", "", ErrEmptySegment},
		{"empty segment tail", "a.", "", ErrEmptySegment},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := NormalizeKey(c.key)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr == nil && got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestArgs(t *testing.T) {
	entries, err := Args([]string{"--a.b=1", "-c=", "d.e=f"})
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "a.b", Value: "1", Layer: LayerArgs},
		{Key: "c", Value: "", Layer: LayerArgs}, // 空值合法
		{Key: "d.e", Value: "f", Layer: LayerArgs},
	}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(entries), len(want))
	}
	for i, e := range entries {
		if e != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, e, want[i])
		}
	}
	if _, err := Args([]string{"no-equals"}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("arg without '=' err = %v, want ErrEmptyKey", err)
	}
}

func TestEnv(t *testing.T) {
	cases := []struct {
		name    string
		prefix  string
		environ []string
		want    []Entry
		wantErr error
	}{
		{"rule1 single underscore", "", []string{"A_B_C=x"},
			[]Entry{{Key: "a.b.c", Value: "x", Layer: LayerEnv}}, nil},
		{"rule2 double underscore", "", []string{"A__B_C=y"},
			[]Entry{{Key: "a.b.c", Value: "y", Layer: LayerEnv}}, nil},
		{"ambiguity", "", []string{"A_B_C=x", "A__B_C=y"}, nil, ErrEnvAmbiguous},
		{"ambiguity short", "", []string{"A_B=x", "A__B=y"}, nil, ErrEnvAmbiguous},
		{"prefix filter", "APP_", []string{"APP_A_B=1", "OTHER=2"},
			[]Entry{{Key: "a.b", Value: "1", Layer: LayerEnv}}, nil},
		{"empty value", "", []string{"A_B="},
			[]Entry{{Key: "a.b", Value: "", Layer: LayerEnv}}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entries, err := Env(c.prefix, c.environ)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr != nil {
				return
			}
			if len(entries) != len(c.want) {
				t.Fatalf("got %v, want %v", entries, c.want)
			}
			for i, e := range entries {
				if e != c.want[i] {
					t.Fatalf("entry %d = %+v, want %+v", i, e, c.want[i])
				}
			}
		})
	}
}
