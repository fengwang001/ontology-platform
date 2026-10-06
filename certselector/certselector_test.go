package certselector

import (
	"errors"
	"testing"
)

func ec(id string, names []string, before, after int64) Certificate {
	return Certificate{ID: id, Names: names, Key: ECDSA, NotBefore: before, NotAfter: after}
}

func rsaCert(id string, names []string, before, after int64) Certificate {
	return Certificate{ID: id, Names: names, Key: RSA, NotBefore: before, NotAfter: after}
}

func both() map[KeyType]bool {
	return map[KeyType]bool{ECDSA: true, RSA: true}
}

func TestValidityBoundaries(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c", []string{"a.example"}, 10, 20))

	if _, err := s.Select("a.example", both(), 9); !errors.Is(err, ErrExpired) {
		t.Fatalf("before NotBefore: want ErrExpired, got %v", err)
	}
	sel, err := s.Select("a.example", both(), 10)
	if err != nil || sel.Certificate.ID != "c" || sel.Source != SourceExact {
		t.Fatalf("at NotBefore: want exact c, got %+v %v", sel, err)
	}
	if _, err := s.Select("a.example", both(), 20); !errors.Is(err, ErrExpired) {
		t.Fatalf("at NotAfter (half-open): want ErrExpired, got %v", err)
	}
	if sel, err := s.Select("a.example", both(), 19); err != nil || sel.Certificate.ID != "c" {
		t.Fatalf("one before NotAfter: got %v %v", sel, err)
	}
}

func TestWildcardMatching(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("w", []string{"*.example.com"}, 0, 100))

	cases := []struct {
		name string
		ok   bool
	}{
		{"foo.example.com", true},
		{"example.com", false},       // zero extra labels
		{"a.b.example.com", false},   // two extra labels
		{"x.y.z.example.com", false}, // more extra labels
	}
	for _, tc := range cases {
		sel, err := s.Select(tc.name, both(), 50)
		if tc.ok {
			if err != nil || sel.Source != SourceWildcard || sel.Certificate.ID != "w" {
				t.Errorf("%s: want wildcard w, got %+v %v", tc.name, sel, err)
			}
		} else {
			if !errors.Is(err, ErrNoMatch) {
				t.Errorf("%s: want ErrNoMatch, got %+v %v", tc.name, sel, err)
			}
		}
	}
}

func TestInvalidWildcardSANs(t *testing.T) {
	s := NewSelector()
	bad := []string{
		"*", "*..com", "*.com", "a.*.com", "a*.com", "*a.com", "*.example.*",
	}
	for _, san := range bad {
		if err := s.Add(ec("x-"+san, []string{san}, 0, 10)); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Add(%q): want ErrInvalidArgument, got %v", san, err)
		}
	}
}

func TestInvalidClientNames(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c", []string{"a.example"}, 0, 100))
	for _, name := range []string{".", "a..example", "a.exa mple", ".foo"} {
		if _, err := s.Select(name, both(), 5); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("Select(%q): want ErrInvalidArgument, got %v", name, err)
		}
	}

	long := make([]byte, 64)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := s.Select(string(long)+".com", both(), 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("64-char label: want invalid, got %v", err)
	}

	overlong := make([]byte, 249)
	for i := range overlong {
		overlong[i] = 'a'
	}
	if _, err := s.Select(string(overlong)+".example.org", both(), 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("262-byte name: want invalid, got %v", err)
	}
}

func TestExactBeatsUsableWildcard(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, rsaCert("exact", []string{"host.site.example"}, 0, 5)) // expired
	mustAdd(t, s, ec("wild", []string{"*.site.example"}, 0, 1000))       // usable
	_, err := s.Select("host.site.example", both(), 50)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("expired exact must suppress usable wildcard: got %v", err)
	}
}

func TestDefaultFallbackBoundary(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("w", []string{"*.example.com"}, 0, 100))
	mustAdd(t, s, ec("d", []string{"default.invalid"}, 0, 100))
	if err := s.SetDefault("d"); err != nil {
		t.Fatal(err)
	}

	// Name with a matching cert never falls back, even if unusable.
	mustAdd(t, s, rsaCert("exp", []string{"gone.example.com"}, 0, 1))
	if _, err := s.Select("gone.example.com", both(), 50); !errors.Is(err, ErrExpired) {
		t.Fatalf("matching expired name: want expired, got %v", err)
	}

	// Unrelated name falls back to default.
	sel, err := s.Select("other.net", both(), 50)
	if err != nil || sel.Source != SourceDefault || sel.Certificate.ID != "d" {
		t.Fatalf("unrelated name: want default d, got %+v %v", sel, err)
	}

	// Empty name falls back too.
	sel, err = s.Select("", both(), 50)
	if err != nil || sel.Source != SourceDefault || sel.Certificate.ID != "d" {
		t.Fatalf("empty name: want default d, got %+v %v", sel, err)
	}

	// Default required to exist.
	if err := s.SetDefault("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetDefault missing: want not found, got %v", err)
	}
}

func TestNoDefaultWithoutMatch(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c", []string{"a.example"}, 0, 100))
	if _, err := s.Select("b.example", both(), 50); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("want no match, got %v", err)
	}
	if _, err := s.Select("", both(), 50); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("empty name no default: want no match, got %v", err)
	}
}

func TestDefaultUnavailableReasons(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, rsaCert("d", []string{"default"}, 0, 10))
	if err := s.SetDefault("d"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Select("", both(), 50); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired default: want expired, got %v", err)
	}
	s.Remove("d")
	s.Add(rsaCert("d2", []string{"default2"}, 0, 100))
	s.SetDefault("d2")
	if _, err := s.Select("", map[KeyType]bool{ECDSA: true}, 50); !errors.Is(err, ErrUnsupportedKey) {
		t.Fatalf("rsa default with ec-only client: want unsupported key, got %v", err)
	}
}

func TestRemoveDefault(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("d", []string{"default"}, 0, 100))
	mustAdd(t, s, ec("o", []string{"other"}, 0, 100))
	s.SetDefault("d")

	s.ClearDefault()
	if _, ok := s.Default(); ok {
		t.Fatal("ClearDefault failed")
	}

	s.SetDefault("d")
	if err := s.Remove("d"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Default(); ok {
		t.Fatal("removing default cert must clear default")
	}
	if _, err := s.Select("", both(), 5); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("after remove: want no match, got %v", err)
	}
	if err := s.Remove("d"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("remove missing: want not found, got %v", err)
	}
}

func TestPreferenceOrder(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, rsaCert("rsa-late", []string{"h.example"}, 0, 200))
	mustAdd(t, s, ec("ec-early", []string{"h.example"}, 0, 100))
	mustAdd(t, s, ec("ec-late", []string{"h.example"}, 0, 300))

	sel, err := s.Select("h.example", both(), 5)
	if err != nil || sel.Certificate.ID != "ec-late" {
		t.Fatalf("ec with latest NotAfter expected, got %+v %v", sel, err)
	}

	s2 := NewSelector()
	mustAdd(t, s2, ec("b", []string{"h"}, 0, 100))
	mustAdd(t, s2, ec("a", []string{"h"}, 0, 100))
	sel, err = s2.Select("h", both(), 5)
	if err != nil || sel.Certificate.ID != "a" {
		t.Fatalf("tie broken by smaller ID, got %+v %v", sel, err)
	}

	s3 := NewSelector()
	mustAdd(t, s3, ec("e", []string{"h"}, 0, 100))
	mustAdd(t, s3, rsaCert("r", []string{"h"}, 0, 1000))
	sel, err = s3.Select("h", map[KeyType]bool{RSA: true}, 5)
	if err != nil || sel.Certificate.ID != "r" {
		t.Fatalf("ec unsupported: rsa should win, got %+v %v", sel, err)
	}
}

func TestUnsupportedKeyBeatsExpired(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, rsaCert("expired", []string{"h"}, 0, 1))
	mustAdd(t, s, rsaCert("validrsa", []string{"h"}, 0, 100))
	if _, err := s.Select("h", map[KeyType]bool{ECDSA: true}, 50); !errors.Is(err, ErrUnsupportedKey) {
		t.Fatalf("mixed expired + valid-unsupported: want unsupported, got %v", err)
	}
}

func TestAddValidationAndConflict(t *testing.T) {
	s := NewSelector()
	base := ec("c", []string{"a.example"}, 0, 100)
	if err := s.Add(base); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(base); !errors.Is(err, ErrConflict) {
		t.Fatalf("dup id: want conflict, got %v", err)
	}
	noNames := base
	noNames.ID = "x"
	noNames.Names = nil
	if err := s.Add(noNames); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty names: want invalid, got %v", err)
	}
	badInterval := base
	badInterval.ID = "y"
	badInterval.NotBefore = 100
	badInterval.NotAfter = 100
	if err := s.Add(badInterval); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty interval: want invalid, got %v", err)
	}
}

func TestSelectInvalidArgumentPriority(t *testing.T) {
	s := NewSelector()
	if _, err := s.Select("", nil, 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty supported set: want invalid, got %v", err)
	}
	if _, err := s.Select("", both(), -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative time: want invalid, got %v", err)
	}
	if _, err := s.Select("..bad", nil, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("multiple violations: want invalid, got %v", err)
	}
}

func TestNameNormalization(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c", []string{"Host.Example"}, 0, 100))
	sel, err := s.Select("HOST.example.", both(), 5)
	if err != nil || sel.Certificate.ID != "c" {
		t.Fatalf("case/trailing-dot normalization: got %+v %v", sel, err)
	}
}

func TestRejectedOpsDoNotMutate(t *testing.T) {
	s := NewSelector()
	mustAdd(t, s, ec("c", []string{"a.example"}, 0, 100))
	s.SetDefault("c")
	bad := ec("q", []string{"bad..name"}, 0, 100)
	if err := s.Add(bad); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("want invalid, got %v", err)
	}
	if _, ok := s.Default(); !ok {
		t.Fatal("failed Add must not clear default")
	}
	if err := s.SetDefault("q"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
	if id, ok := s.Default(); !ok || id != "c" {
		t.Fatalf("failed SetDefault must keep old default, got %q %v", id, ok)
	}
}

func mustAdd(t *testing.T, s *Selector, cert Certificate) {
	t.Helper()
	if err := s.Add(cert); err != nil {
		t.Fatalf("Add(%s): %v", cert.ID, err)
	}
}
