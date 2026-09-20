package negotiate

import (
	"errors"
	"testing"
)

// Semantics 1: q values sort descending; default q is 1;
// 0, 1 and up to three decimals are supported.
func TestSelectQValueOrdering(t *testing.T) {
	offers := []string{"text/plain", "text/html", "application/json"}
	offer, rule, err := Select("text/plain;q=0.5, text/html;q=0.333, application/json;q=0.8", offers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offer != "application/json" || rule != "application/json;q=0.8" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	offer, _, err = Select("text/html;q=1, text/plain;q=0", offers)
	if err != nil || offer != "text/html" {
		t.Fatalf("got (%q, %v)", offer, err)
	}

	// Default q=1 beats explicit lower q.
	offer, _, err = Select("text/html;q=0.9, text/plain", offers)
	if err != nil || offer != "text/plain" {
		t.Fatalf("got (%q, %v)", offer, err)
	}
}

// Semantics 2: for one offer, a more specific rule wins over a less
// specific one even when its q is lower.
func TestSelectSpecificityBeatsQ(t *testing.T) {
	offers := []string{"text/html"}
	offer, rule, err := Select("text/html;q=0.1, text/*;q=0.9, */*;q=1", offers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offer != "text/html" || rule != "text/html;q=0.1" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	// Type wildcard beats full wildcard for the same offer.
	_, rule, err = Select("text/*;q=0.2, */*;q=0.9", offers)
	if err != nil || rule != "text/*;q=0.2" {
		t.Fatalf("got rule %q, err %v", rule, err)
	}
}

// Semantics 3: q=0 is an explicit rejection, but only for its target.
func TestSelectQZeroRejects(t *testing.T) {
	offers := []string{"text/html", "text/plain"}
	offer, _, err := Select("text/html;q=0, */*;q=1", offers)
	if err != nil || offer != "text/plain" {
		t.Fatalf("got (%q, %v)", offer, err)
	}

	_, _, err = Select("text/html;q=0", []string{"text/html"})
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("want ErrNotAcceptable, got %v", err)
	}
}

// Semantics 4: equal specificity and q -> server offer order wins,
// deterministically.
func TestSelectTieBreaksByServerOrder(t *testing.T) {
	offers := []string{"text/plain", "text/html"}
	for i := 0; i < 10; i++ {
		offer, _, err := Select("text/*;q=0.7", offers)
		if err != nil || offer != "text/plain" {
			t.Fatalf("run %d: got (%q, %v)", i, offer, err)
		}
	}
}

// Semantics 5: parameters before q participate in matching;
// parameters after q are ignored.
func TestSelectParameters(t *testing.T) {
	offers := []string{"text/html;level=1", "text/html"}
	offer, rule, err := Select("text/html;level=1;q=0.5", offers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if offer != "text/html;level=1" || rule != "text/html;level=1;q=0.5" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	// Offer without the parameter does not match the parametrized rule.
	_, _, err = Select("text/html;level=1;q=0.5", []string{"text/html"})
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("want ErrNotAcceptable, got %v", err)
	}

	// accept-ext after q is ignored, even if malformed-looking.
	offer, _, err = Select("text/html;q=0.5;ignored;also-ignored", []string{"text/html"})
	if err != nil || offer != "text/html" {
		t.Fatalf("got (%q, %v)", offer, err)
	}
}

// Semantics 6: empty/blank Accept means */*; empty offers -> error.
func TestSelectEmptyDefaults(t *testing.T) {
	for _, accept := range []string{"", "   ", " \t "} {
		offer, rule, err := Select(accept, []string{"text/html", "text/plain"})
		if err != nil {
			t.Fatalf("accept %q: unexpected error: %v", accept, err)
		}
		if offer != "text/html" || rule != "*/*" {
			t.Fatalf("accept %q: got (%q, %q)", accept, offer, rule)
		}
	}

	_, _, err := Select("text/html", nil)
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("want ErrNotAcceptable, got %v", err)
	}
}

// Semantics 8: Select never mutates offers and is repeatable.
func TestSelectDeterministicAndNonMutating(t *testing.T) {
	offers := []string{"text/html;level=1", "text/plain", "application/json"}
	snapshot := append([]string(nil), offers...)
	accept := "text/*;q=0.6, application/json;q=0.9"

	o1, r1, err1 := Select(accept, offers)
	o2, r2, err2 := Select(accept, offers)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if o1 != o2 || r1 != r2 {
		t.Fatalf("non-deterministic: (%q,%q) vs (%q,%q)", o1, r1, o2, r2)
	}
	for i := range offers {
		if offers[i] != snapshot[i] {
			t.Fatalf("offers mutated at %d: %q != %q", i, offers[i], snapshot[i])
		}
	}
}
