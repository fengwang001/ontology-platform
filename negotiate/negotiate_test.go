package negotiate_test

import (
	"errors"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"ontology/mtype"
	"ontology/negotiate"
	"ontology/rank"
)

func ptr(s string) *string { return &s }

func TestSelect(t *testing.T) {
	tests := []struct {
		name       string
		accept     *string
		candidates []string
		want       int
		wantErr    error
	}{
		{"specificity", ptr("text/*;q=0.5, text/plain;q=0.5"), []string{"text/html", "text/plain"}, 1, nil},
		{"order independent", ptr("text/plain;q=0.5, text/*;q=0.5"), []string{"text/html", "text/plain"}, 1, nil},
		{"q first", ptr("text/*;q=0.2, text/plain;q=0.1"), []string{"text/plain", "text/html"}, 1, nil},
		{"q zero only", ptr("text/plain;q=0"), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"q zero blocks wildcard", ptr("*/*;q=0"), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"invalid q dropped", ptr("text/plain;q=bad, application/json;q=1"), []string{"text/plain", "application/json"}, 1, nil},
		{"params exact", ptr("text/plain;format=flowed"), []string{"text/plain;format=flowed"}, 0, nil},
		{"params missing", ptr("text/plain;format=flowed"), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"params value case", ptr("text/plain;format=flowed"), []string{"text/plain;format=Flowed"}, -1, negotiate.ErrNotAcceptable},
		{"server tie first", ptr("text/plain, application/json"), []string{"application/json", "text/plain"}, 0, nil},
		{"server tie second", ptr("text/plain, application/json"), []string{"text/plain", "application/json"}, 0, nil},
		{"missing", nil, []string{"text/plain"}, 0, nil},
		{"empty", ptr(""), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"whitespace", ptr("   "), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"no fallback", ptr("application/json"), []string{"text/plain"}, -1, negotiate.ErrNotAcceptable},
		{"quoted semicolon", ptr(`text/plain;x="a;b"`), []string{`text/plain;x="a;b"`}, 0, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.candidates)
			got, err := negotiate.Select(tc.accept, tc.candidates)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr == nil && got.Index != tc.want {
				t.Fatalf("index = %d, want %d", got.Index, tc.want)
			}
			if !slices.Equal(before, tc.candidates) {
				t.Fatalf("candidates mutated: %v", tc.candidates)
			}
		})
	}
}

func TestNormalizationAndParams(t *testing.T) {
	left, err := mtype.Parse(`TEXT/Plain;B="2";A=1`, 0)
	if err != nil {
		t.Fatal(err)
	}
	right, err := mtype.Parse(`text/plain;a=1;b=2`, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !mtype.EqualParams(left.Params, right.Params) {
		t.Fatal("parameter order and names should normalize")
	}
	quoted, err := mtype.Parse(`text/plain;x="a;b\"c"`, 2)
	if err != nil || quoted.Params["x"] != `a;b"c` {
		t.Fatalf("quoted value = %#v, err = %v", quoted.Params, err)
	}
}

func TestInvalidQ(t *testing.T) {
	for _, raw := range []string{"1.5", "abc", "0.1234"} {
		if _, err := rank.ParseAcceptItem("text/plain;q="+raw, 4); !errors.Is(err, rank.ErrInvalidQ) {
			t.Fatalf("%s: err = %v", raw, err)
		}
	}
}

func TestParseErrors(t *testing.T) {
	kinds := []error{mtype.ErrMissingSlash, mtype.ErrEmptySubtype, mtype.ErrMissingEqual, mtype.ErrUnclosedQuote, rank.ErrInvalidQ}
	for i, input := range []string{"text", "text/", "text/plain;x", `text/plain;x="`, "text/plain;q=abc"} {
		_, err := rank.ParseAcceptItem(input, 9)
		if !errors.Is(err, kinds[i]) || !strings.Contains(err.Error(), " 9") {
			t.Fatalf("%s: %v, want %v with index", input, err, kinds[i])
		}
	}
}

func TestShuffleStable(t *testing.T) {
	candidates := []string{"application/json", "text/html", "text/plain"}
	base := "text/html, application/json, text/plain"
	want, err := negotiate.Select(&base, candidates)
	if err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewSource(99))
	for range 50 {
		items := strings.Split(base, ", ")
		random.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		accept := strings.Join(items, ", ")
		got, err := negotiate.Select(&accept, candidates)
		if err != nil || got.Index != want.Index {
			t.Fatalf("got %d, err %v; want %d", got.Index, err, want.Index)
		}
	}
}
