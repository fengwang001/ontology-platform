package trim

import (
	"errors"
	"testing"

	"ontology/netmatch"
)

func TestTrim(t *testing.T) {
	cases := []struct {
		name, chain, fwd, want string
		stopAt                 int
	}{
		{"basic", "1.2.3.4, 10.0.0.1, 10.0.0.2", "", "1.2.3.4", 1},
		{"forged", "9.9.9.9, 1.2.3.4, 10.0.0.1", "", "1.2.3.4", 2},
		{"all-trusted", "10.0.0.3, 10.0.0.1", "", "10.0.0.3", 1},
		{"unknown-near", "", "for=10.0.0.1, for=unknown", "", 2},
		{"unknown-far", "", "for=_, for=10.0.0.1", "", 1},
		{"both-headers", "1.2.3.4, 10.0.0.1", "for=10.0.0.1", "1.2.3.4", 1},
		{"mapped-v6", "[::ffff:10.0.0.1]:443, 10.0.0.2", "", "::ffff:10.0.0.1", 1},
		{"direct", "", "", "192.0.2.1", 0},
	}
	for _, tc := range cases {
		tr, err := New(16, 8, []string{"10.0.0.0/8"})
		if err != nil {
			t.Fatalf("%s: New: %v", tc.name, err)
		}
		r, err := tr.Trim("192.0.2.1", tc.chain, tc.fwd)
		if err != nil {
			t.Fatalf("%s: Trim: %v", tc.name, err)
		}
		if r.StoppedAt != tc.stopAt {
			t.Errorf("%s: StoppedAt=%d want %d", tc.name, r.StoppedAt, tc.stopAt)
		}
		a, _ := netmatch.ParseAddr(tc.want)
		if r.HasClient != (tc.want != "") {
			t.Errorf("%s: HasClient=%v want %q", tc.name, r.HasClient, tc.want)
		} else if r.HasClient && r.Client != a {
			t.Errorf("%s: client=%v want %v", tc.name, r.Client, a)
		}
	}
}

func TestNetmatch(t *testing.T) {
	cases := []struct{ cidr, addr string }{
		{"10.0.0.0/8", "::ffff:10.0.0.1"},   // 映射地址被 IPv4 网段包含
		{"::ffff:10.0.0.0/104", "10.0.0.1"}, // IPv4 地址被映射网段包含
		{"::ffff:10.0.0.0/104", "::ffff:10.0.0.1"},
		{"2001:db8::/32", "2001:db8::1"},
		{"10.0.0.0/8", "10.255.255.255"},
	}
	for _, tc := range cases {
		p, err := netmatch.ParsePrefix(tc.cidr)
		if err != nil {
			t.Fatalf("ParsePrefix(%s): %v", tc.cidr, err)
		}
		a, err := netmatch.ParseAddr(tc.addr)
		if err != nil || !netmatch.Contains(p, a) {
			t.Errorf("Contains(%s, %s)=false, err=%v", tc.cidr, tc.addr, err)
		}
	}
	p, _ := netmatch.ParsePrefix("10.0.0.0/8")
	if a, _ := netmatch.ParseAddr("11.0.0.1"); netmatch.Contains(p, a) {
		t.Error("11.0.0.1 should not be in 10.0.0.0/8")
	}
	z1, e1 := netmatch.ParseAddr("fe80::1%eth0")
	z2, _ := netmatch.ParseAddr("[fe80::1]:80")
	if e1 != nil || z1 != z2 {
		t.Error("zone/port must be stripped")
	}
	for _, bad := range []string{"10.0.0.0/33", "2001:db8::/129", "x/8"} {
		if _, err := netmatch.ParsePrefix(bad); !errors.Is(err, netmatch.ErrBadCIDR) {
			t.Errorf("ParsePrefix(%s) err=%v", bad, err)
		}
	}
	if _, err := netmatch.ParseAddr("nope"); !errors.Is(err, netmatch.ErrBadAddr) {
		t.Errorf("ParseAddr err=%v", err)
	}
}

func TestLimitsAndErrors(t *testing.T) {
	if _, err := New(4, 1, []string{"10.0.0.0/8", "11.0.0.0/8"}); !errors.Is(err, ErrTooManyCIDRs) {
		t.Errorf("cidr limit err=%v", err)
	}
	if _, err := New(4, 2, []string{"10.0.0.0/8", "11.0.0.0/8"}); err != nil {
		t.Errorf("at-limit must pass: %v", err)
	}
	tr, _ := New(2, 4, []string{"10.0.0.0/8"})
	if r, err := tr.Trim("192.0.2.1", "1.2.3.4, 10.0.0.1, 10.0.0.2", ""); !errors.Is(err, ErrTooManyHops) || r != (Result{}) {
		t.Errorf("hop limit: err=%v r=%+v", err, r)
	}
	tr, _ = New(8, 4, []string{"10.0.0.0/8"})
	_, err := tr.Trim("192.0.2.1", "1.2.3.4, bogus, 10.0.0.1", "")
	var ae *AddrError
	if !errors.As(err, &ae) || ae.Hop != 2 || !errors.Is(err, ErrBadAddr) {
		t.Errorf("bad addr: err=%v", err)
	}
	for _, pair := range [][2]error{{ErrBadAddr, ErrTooManyHops}, {ErrBadCIDR, ErrTooManyCIDRs}, {ErrTooManyHops, ErrTooManyCIDRs}} {
		if errors.Is(pair[0], pair[1]) {
			t.Errorf("%v and %v must be distinguishable", pair[0], pair[1])
		}
	}
	tr, _ = New(8, 4, []string{"10.0.0.0/8", "192.168.0.0/16"})
	if _, err = tr.Trim("192.0.2.1", "1.1.1.1, 2.2.2.2, 3.3.3.3", ""); err != nil {
		t.Fatal(err)
	}
	if tr.checks > 3*2 {
		t.Errorf("checks=%d > n*m=6", tr.checks)
	}
	tr, _ = New(8, 4, []string{"10.0.0.0/8", "192.168.0.0/16"})
	if _, err = tr.Trim("192.0.2.1", "10.0.0.1, 10.0.0.2, 9.9.9.9", ""); err != nil {
		t.Fatal(err)
	}
	if tr.checks > 2 {
		t.Errorf("first-hop-stop checks=%d > m=2", tr.checks)
	}
}
