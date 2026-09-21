package iproute

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, tb *Table, cidr, next string) {
	t.Helper()
	if err := tb.Add(cidr, next); err != nil {
		t.Fatalf("Add(%q, %q): %v", cidr, next, err)
	}
}

func mustLookup(t *testing.T, tb *Table, ip, wantNext, wantMatch string) {
	t.Helper()
	next, matched, err := tb.Lookup(ip)
	if err != nil {
		t.Fatalf("Lookup(%q): %v", ip, err)
	}
	if next != wantNext || matched != wantMatch {
		t.Fatalf("Lookup(%q) = (%q, %q), want (%q, %q)",
			ip, next, matched, wantNext, wantMatch)
	}
}

func TestLongestPrefixWins(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	mustAdd(t, tb, "10.1.2.0/24", "hop-c")

	mustLookup(t, tb, "10.1.2.3", "hop-c", "10.1.2.0/24")
	mustLookup(t, tb, "10.1.3.3", "hop-b", "10.1.0.0/16")
	mustLookup(t, tb, "10.9.9.9", "hop-a", "10.0.0.0/8")
}

func TestDefaultRoute(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	mustLookup(t, tb, "1.2.3.4", "gw", "0.0.0.0/0")
	mustLookup(t, tb, "255.255.255.255", "gw", "0.0.0.0/0")
	mustLookup(t, tb, "0.0.0.0", "gw", "0.0.0.0/0")
}

func TestHostRoute(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	mustAdd(t, tb, "192.168.1.1/32", "direct")
	mustLookup(t, tb, "192.168.1.1", "direct", "192.168.1.1/32")
	mustLookup(t, tb, "192.168.1.2", "gw", "0.0.0.0/0")
}

func TestNoRoute(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	_, _, err := tb.Lookup("11.0.0.1")
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("want ErrNoRoute, got %v", err)
	}
}

func TestLookupInvalidIP(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	if _, _, err := tb.Lookup("010.0.0.1"); !errors.Is(err, ErrInvalidIP) {
		t.Fatalf("want ErrInvalidIP, got %v", err)
	}
}

func TestAddDuplicate(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	if err := tb.Add("10.0.0.0/8", "hop-b"); !errors.Is(err, ErrDuplicateRoute) {
		t.Fatalf("want ErrDuplicateRoute, got %v", err)
	}
	// Original next hop must be preserved.
	mustLookup(t, tb, "10.1.2.3", "hop-a", "10.0.0.0/8")
}

func TestAddEmptyNext(t *testing.T) {
	tb := New()
	if err := tb.Add("10.0.0.0/8", ""); !errors.Is(err, ErrEmptyNext) {
		t.Fatalf("want ErrEmptyNext, got %v", err)
	}
}

func TestAddInvalidCIDR(t *testing.T) {
	tb := New()
	if err := tb.Add("192.168.1.1/24", "hop"); !errors.Is(err, ErrHostBitsSet) {
		t.Fatalf("want ErrHostBitsSet, got %v", err)
	}
	if err := tb.Add("10.0.0.0/08", "hop"); !errors.Is(err, ErrInvalidMask) {
		t.Fatalf("want ErrInvalidMask, got %v", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	tb := New()
	if err := tb.Delete("10.0.0.0/8"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("want ErrRouteNotFound, got %v", err)
	}
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	// Same network, different length: still missing.
	if err := tb.Delete("10.0.0.0/16"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("want ErrRouteNotFound, got %v", err)
	}
}

func TestDeleteUnshadowsShorterPrefix(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	mustLookup(t, tb, "10.1.2.3", "hop-b", "10.1.0.0/16")

	if err := tb.Delete("10.1.0.0/16"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mustLookup(t, tb, "10.1.2.3", "hop-a", "10.0.0.0/8")

	// Deleting again must fail.
	if err := tb.Delete("10.1.0.0/16"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("want ErrRouteNotFound, got %v", err)
	}
}

func TestReAddAfterDelete(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	if err := tb.Delete("10.0.0.0/8"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	mustAdd(t, tb, "10.0.0.0/8", "hop-b")
	mustLookup(t, tb, "10.1.2.3", "hop-b", "10.0.0.0/8")
}
