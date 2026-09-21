package iproute

import (
	"errors"
	"testing"
)

func mustAdd(t *testing.T, tb *Table, cidr, next string) {
	t.Helper()
	if err := tb.Add(cidr, next); err != nil {
		t.Fatalf("Add(%q, %q) unexpected error: %v", cidr, next, err)
	}
}

func mustLookup(t *testing.T, tb *Table, ip string) (string, string) {
	t.Helper()
	next, matched, err := tb.Lookup(ip)
	if err != nil {
		t.Fatalf("Lookup(%q) unexpected error: %v", ip, err)
	}
	return next, matched
}

func TestLongestPrefixWins(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	mustAdd(t, tb, "10.1.2.0/24", "hop-c")
	next, matched := mustLookup(t, tb, "10.1.2.3")
	if next != "hop-c" || matched != "10.1.2.0/24" {
		t.Errorf("got (%q, %q), want (hop-c, 10.1.2.0/24)", next, matched)
	}
	next, matched = mustLookup(t, tb, "10.1.3.3")
	if next != "hop-b" || matched != "10.1.0.0/16" {
		t.Errorf("got (%q, %q), want (hop-b, 10.1.0.0/16)", next, matched)
	}
	next, matched = mustLookup(t, tb, "10.9.9.9")
	if next != "hop-a" || matched != "10.0.0.0/8" {
		t.Errorf("got (%q, %q), want (hop-a, 10.0.0.0/8)", next, matched)
	}
}

func TestDefaultRoute(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	for _, ip := range []string{"0.0.0.0", "1.2.3.4", "255.255.255.255"} {
		next, matched := mustLookup(t, tb, ip)
		if next != "gw" || matched != "0.0.0.0/0" {
			t.Errorf("Lookup(%q) = (%q, %q), want (gw, 0.0.0.0/0)", ip, next, matched)
		}
	}
}

func TestHostRoute32(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "192.168.1.1/32", "direct")
	next, matched := mustLookup(t, tb, "192.168.1.1")
	if next != "direct" || matched != "192.168.1.1/32" {
		t.Errorf("got (%q, %q), want (direct, 192.168.1.1/32)", next, matched)
	}
	if _, _, err := tb.Lookup("192.168.1.2"); !errors.Is(err, ErrNoRoute) {
		t.Errorf("Lookup(192.168.1.2) error = %v, want ErrNoRoute", err)
	}
}

func TestNoRouteError(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	next, matched, err := tb.Lookup("192.168.0.1")
	if !errors.Is(err, ErrNoRoute) {
		t.Fatalf("error = %v, want ErrNoRoute", err)
	}
	if next != "" || matched != "" {
		t.Errorf("got (%q, %q), want empty results on miss", next, matched)
	}
}

func TestLookupInvalidIP(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "0.0.0.0/0", "gw")
	if _, _, err := tb.Lookup("010.0.0.1"); !errors.Is(err, ErrInvalidIP) {
		t.Errorf("error = %v, want ErrInvalidIP", err)
	}
}

func TestAddDuplicate(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	if err := tb.Add("10.0.0.0/8", "hop-b"); !errors.Is(err, ErrDuplicateRoute) {
		t.Fatalf("error = %v, want ErrDuplicateRoute", err)
	}
	if next, _ := mustLookup(t, tb, "10.1.2.3"); next != "hop-a" {
		t.Errorf("next = %q, want hop-a (no silent overwrite)", next)
	}
	mustAdd(t, tb, "10.0.0.0/16", "hop-c")
}

func TestAddEmptyNext(t *testing.T) {
	tb := New()
	if err := tb.Add("10.0.0.0/8", ""); !errors.Is(err, ErrEmptyNext) {
		t.Fatalf("error = %v, want ErrEmptyNext", err)
	}
	if _, _, err := tb.Lookup("10.1.2.3"); !errors.Is(err, ErrNoRoute) {
		t.Errorf("route must not be installed on empty next, error = %v", err)
	}
}

func TestAddInvalidCIDR(t *testing.T) {
	tb := New()
	if err := tb.Add("192.168.1.1/24", "hop"); !errors.Is(err, ErrHostBitsSet) {
		t.Errorf("error = %v, want ErrHostBitsSet", err)
	}
	if err := tb.Add("10.0.0.0/08", "hop"); !errors.Is(err, ErrInvalidCIDR) {
		t.Errorf("error = %v, want ErrInvalidCIDR", err)
	}
}

func TestDeleteMissing(t *testing.T) {
	tb := New()
	if err := tb.Delete("10.0.0.0/8"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("error = %v, want ErrRouteNotFound", err)
	}
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	if err := tb.Delete("10.0.0.0/16"); !errors.Is(err, ErrRouteNotFound) {
		t.Errorf("error = %v, want ErrRouteNotFound", err)
	}
}

func TestDeleteRevealsShorterPrefix(t *testing.T) {
	tb := New()
	mustAdd(t, tb, "10.0.0.0/8", "hop-a")
	mustAdd(t, tb, "10.1.0.0/16", "hop-b")
	if _, matched := mustLookup(t, tb, "10.1.2.3"); matched != "10.1.0.0/16" {
		t.Fatalf("before delete matched %q, want 10.1.0.0/16", matched)
	}
	if err := tb.Delete("10.1.0.0/16"); err != nil {
		t.Fatalf("Delete unexpected error: %v", err)
	}
	next, matched := mustLookup(t, tb, "10.1.2.3")
	if next != "hop-a" || matched != "10.0.0.0/8" {
		t.Errorf("after delete got (%q, %q), want (hop-a, 10.0.0.0/8)", next, matched)
	}
	if err := tb.Delete("10.1.0.0/16"); !errors.Is(err, ErrRouteNotFound) {
		t.Errorf("second delete error = %v, want ErrRouteNotFound", err)
	}
	mustAdd(t, tb, "10.1.0.0/16", "hop-b2")
	if next, _ := mustLookup(t, tb, "10.1.2.3"); next != "hop-b2" {
		t.Errorf("after re-add next = %q, want hop-b2", next)
	}
}

func TestDeleteInvalidCIDR(t *testing.T) {
	tb := New()
	if err := tb.Delete("10.0.0.1/8"); !errors.Is(err, ErrHostBitsSet) {
		t.Errorf("error = %v, want ErrHostBitsSet", err)
	}
}
