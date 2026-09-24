package negotiate_test

import (
	"errors"
	"math/rand"
	"strings"
	"testing"

	"ontology/mtype"
	"ontology/negotiate"
)

func ptr(s string) *string { return &s }

func TestSelect(t *testing.T) {
	cases := []struct {
		name    string
		accept  *string
		offered []string
		want    string
		wantErr error
	}{
		{"spec three levels", ptr("*/*;q=0.8, text/*;q=0.8, text/plain;q=0.8"), []string{"image/png", "text/html", "text/plain"}, "text/plain", nil},
		{"spec accept order free", ptr("text/plain;q=0.8, */*;q=0.8, text/*;q=0.8"), []string{"image/png", "text/html", "text/plain"}, "text/plain", nil},
		{"q beats specificity", ptr("text/*;q=0.9, text/plain;q=0.1"), []string{"text/html", "text/plain"}, "text/html", nil},
		{"q0 sole candidate", ptr("text/plain;q=0"), []string{"text/plain"}, "", negotiate.ErrUnacceptable},
		{"q0 skips to other", ptr("text/plain;q=0, text/html"), []string{"text/plain", "text/html"}, "text/html", nil},
		{"q default is 1", ptr("text/a;q=0.5, text/b"), []string{"text/a", "text/b"}, "text/b", nil},
		{"params equal match", ptr("text/plain;format=flowed"), []string{"text/plain;format=flowed"}, "text/plain;format=flowed", nil},
		{"accept lacks param", ptr("text/plain"), []string{"text/plain;format=flowed"}, "", negotiate.ErrUnacceptable},
		{"candidate lacks param", ptr("text/plain;format=flowed"), []string{"text/plain"}, "", negotiate.ErrUnacceptable},
		{"param value case", ptr("text/plain;format=FLOWED"), []string{"text/plain;format=flowed"}, "", negotiate.ErrUnacceptable},
		{"param name case", ptr("text/plain;FORMAT=flowed"), []string{"text/plain;format=flowed"}, "text/plain;format=flowed", nil},
		{"param order free", ptr("text/plain;b=2;a=1"), []string{"text/plain;a=1;b=2"}, "text/plain;a=1;b=2", nil},
		{"type case normalized", ptr("TEXT/Plain"), []string{"text/plain"}, "text/plain", nil},
		{"tie server order", ptr("text/a, text/b"), []string{"text/b", "text/a"}, "text/b", nil},
		{"tie accept swapped", ptr("text/b, text/a"), []string{"text/b", "text/a"}, "text/b", nil},
		{"tie offered swapped", ptr("text/a, text/b"), []string{"text/a", "text/b"}, "text/a", nil},
		{"missing header", nil, []string{"text/plain"}, "text/plain", nil},
		{"empty header", ptr(""), []string{"text/plain"}, "", negotiate.ErrUnacceptable},
		{"no fallback", ptr("image/png"), []string{"text/plain"}, "", negotiate.ErrUnacceptable},
		{"quoted semicolon", ptr(`text/plain;x="a;b"`), []string{`text/plain;x="a;b"`}, `text/plain;x="a;b"`, nil},
		{"escaped quote", ptr(`text/plain;x="a\"b"`), []string{`text/plain;x="a\"b"`}, `text/plain;x="a\"b"`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := negotiate.Select(tc.accept, tc.offered)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestErrorKinds(t *testing.T) {
	cases := []struct {
		in   string
		kind mtype.Kind
	}{
		{"text", mtype.ErrNoSlash},
		{"text/", mtype.ErrEmptySubtype},
		{"text/plain;fmt", mtype.ErrParamNoEq},
		{`text/plain;x="abc`, mtype.ErrUnclosedQuote},
		{"text/plain;q=1.5", mtype.ErrBadQ},
		{"text/plain;q=abc", mtype.ErrBadQ},
		{"text/plain;q=0.1234", mtype.ErrBadQ},
	}
	seen := map[mtype.Kind]bool{}
	for _, tc := range cases {
		_, err := negotiate.Select(ptr("ok/ok, "+tc.in), []string{"ok/ok"})
		var pe *mtype.Error
		if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.Index != 1 {
			t.Errorf("%s: err = %v, want kind %d at index 1", tc.in, err, tc.kind)
		}
		seen[pe.Kind] = true
	}
	if len(seen) != 5 {
		t.Errorf("error kinds not distinguishable: %v", seen)
	}
}

func TestShuffleStableAndReadOnly(t *testing.T) {
	items := []string{"text/a;q=0.5", "text/b;q=0.9", "text/c;q=0.7", "text/d;q=0.6"}
	offered := []string{"text/a", "text/b", "text/c", "text/d"}
	orig := append([]string(nil), offered...)
	want, err := negotiate.Select(ptr(strings.Join(items, ", ")), offered)
	if err != nil || want != "text/b" {
		t.Fatalf("baseline = %q, %v", want, err)
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 50; i++ {
		r.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
		got, err := negotiate.Select(ptr(strings.Join(items, ", ")), offered)
		if err != nil || got != want {
			t.Fatalf("shuffle %d: got %q, %v", i, got, err)
		}
	}
	again, _ := negotiate.Select(ptr(strings.Join(items, ", ")), offered)
	if again != want {
		t.Fatal("repeated select differs")
	}
	for i := range offered {
		if offered[i] != orig[i] {
			t.Fatalf("offered mutated at %d: %q", i, offered[i])
		}
	}
}
