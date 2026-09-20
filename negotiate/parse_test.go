package negotiate

import (
	"errors"
	"testing"
)

// Semantics 7: syntax errors yield ErrMalformed with empty results.
func TestSelectMalformed(t *testing.T) {
	cases := []string{
		"texthtml",              // missing slash
		"/html",                 // empty type
		"text/",                 // empty subtype
		"text/html/extra",       // double slash
		"text/html;level",       // parameter without '='
		"text/html;=1",          // empty parameter name
		"text/html;q=",          // empty q
		"text/html;q=abc",       // non-numeric q
		"text/html;q=-0.5",      // negative q
		"text/html;q=1.5",       // q > 1
		"text/html;q=2",         // q > 1
		"text/html;q=0.3333",    // more than three decimals
		"text/html;q=1.001",     // > 1 via decimals
		"text/html;q=0.5.3",     // malformed decimals
		"*/html",                // wildcard type with concrete subtype
		"te*xt/html",            // partial wildcard type
		"text/ht*ml",            // partial wildcard subtype
		"text/html,,text/css",   // empty item
		"text/html;q=0.5x",      // trailing junk in q
		"text/html;level=1;q=x", // invalid q after valid params
	}
	for _, accept := range cases {
		offer, rule, err := Select(accept, []string{"text/html"})
		if !errors.Is(err, ErrMalformed) {
			t.Errorf("accept %q: want ErrMalformed, got %v", accept, err)
		}
		if offer != "" || rule != "" {
			t.Errorf("accept %q: want empty results, got (%q, %q)", accept, offer, rule)
		}
	}
}

// Semantics 1 (boundary): valid q forms parse to the expected values.
func TestParseQValid(t *testing.T) {
	cases := map[string]int{
		"0":     0,
		"1":     1000,
		"0.0":   0,
		"1.0":   1000,
		"1.000": 1000,
		"0.5":   500,
		"0.05":  50,
		"0.005": 5,
		"0.333": 333,
		"0.999": 999,
	}
	for input, want := range cases {
		got, err := parseQ(input)
		if err != nil {
			t.Errorf("parseQ(%q): unexpected error %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("parseQ(%q) = %d, want %d", input, got, want)
		}
	}
}

// Case-insensitivity of types, subtypes and parameter names.
func TestSelectCaseInsensitive(t *testing.T) {
	offer, _, err := Select("TEXT/HTML;Level=1", []string{"text/html;level=1"})
	if err != nil || offer != "text/html;level=1" {
		t.Fatalf("got (%q, %v)", offer, err)
	}
}

// Whitespace around items and parameters is tolerated.
func TestSelectWhitespace(t *testing.T) {
	offer, rule, err := Select("  text/html ; q=0.4 , text/plain ", []string{"text/plain", "text/html"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offer != "text/plain" || rule != "text/plain" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}
}
