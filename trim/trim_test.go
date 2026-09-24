package trim

import (
	"errors"
	"reflect"
	"testing"

	"ontology/hoplex"
	"ontology/netmatch"
)

type ac struct {
	s1, s2 string
	ok     bool
}
type tc struct {
	name, peer, chain, kv, wantAddr string
	wantHas                         bool
	wantStop                        int
}

func TestNetmatch(t *testing.T) {
	addrs := []ac{
		{"1.2.3.4", "", true}, {"1.2.3.4:80", "", true}, {"::1", "", true},
		{"[::1]:8080", "", true}, {"fe80::1%eth0", "", true}, {"::ffff:1.2.3.4", "", true},
		{"", "", false}, {"nope", "", false}, {"[::1", "", false}, {"999.1.1.1", "", false},
	}
	for _, c := range addrs {
		_, err := netmatch.ParseAddr(c.s1)
		if (err == nil) != c.ok || (!c.ok && !errors.Is(err, netmatch.ErrBadAddr)) {
			t.Errorf("ParseAddr(%q) err=%v want ok=%v", c.s1, err, c.ok)
		}
	}
	contains := []ac{
		{"10.0.0.0/8", "::ffff:10.0.0.1", true}, // mapped v6 inside v4 cidr
		{"::ffff:10.0.0.0/104", "10.0.0.1", true},
		{"10.0.0.0/8", "10.0.0.1", true}, {"10.0.0.0/8", "11.0.0.1", false},
		{"2001:db8::/32", "2001:db8::1", true}, {"2001:db8::/32", "::ffff:10.0.0.1", false},
	}
	for _, c := range contains {
		p, _ := netmatch.ParseCIDR(c.s1)
		a, _ := netmatch.ParseAddr(c.s2)
		if got := netmatch.Contains(p, a); got != c.ok {
			t.Errorf("Contains(%v,%v)=%v want %v", p, a, got, c.ok)
		}
	}
	for _, bad := range []string{"10.0.0.0/33", "2001:db8::/129", "junk"} {
		if _, err := netmatch.ParseCIDR(bad); !errors.Is(err, netmatch.ErrBadCIDR) {
			t.Errorf("ParseCIDR(%q) err %v not ErrBadCIDR", bad, err)
		}
	}
}

func TestHoplex(t *testing.T) {
	ch := hoplex.ParseChain(" 1.2.3.4, 10.0.0.1 ,,")
	wantCh := []hoplex.Hop{{Addr: "1.2.3.4"}, {Addr: "10.0.0.1"}}
	if !reflect.DeepEqual(ch, wantCh) {
		t.Errorf("ParseChain got %+v", ch)
	}
	fw := hoplex.ParseForwarded(`For="[::1]:443";proto=https, for=unknown, FOR=_hid, for="a;b,c"`)
	want := []hoplex.Hop{
		{Source: hoplex.Forwarded, Addr: "[::1]:443"}, {Source: hoplex.Forwarded, Missing: true},
		{Source: hoplex.Forwarded, Missing: true}, {Source: hoplex.Forwarded, Addr: "a;b,c"},
	}
	if !reflect.DeepEqual(fw, want) {
		t.Errorf("ParseForwarded got %+v", fw)
	}
}

func TestTrim(t *testing.T) {
	cases := []tc{
		{"basic", "", "1.2.3.4, 10.0.0.1, 10.0.0.2", "", "1.2.3.4", true, 0},
		{"forged", "", "9.9.9.9, 1.2.3.4, 10.0.0.1", "", "1.2.3.4", true, 1},
		{"all-trusted", "", "10.0.0.1, 10.0.0.2", "", "10.0.0.1", true, 0},
		{"missing", "", "", "for=1.2.3.4, for=_x", "", false, 1},
		{"both", "", "8.8.8.8", "for=10.0.0.1", "8.8.8.8", true, 0},
		{"direct", "9.9.9.9", "", "", "9.9.9.9", true, -1},
	}
	for _, c := range cases {
		tr, _ := New(8, 4, []string{"10.0.0.0/8"})
		r, err := tr.Client(c.peer, c.chain, c.kv)
		if err != nil || r.HasAddr != c.wantHas || r.Stop != c.wantStop ||
			(c.wantHas && r.Addr.String() != c.wantAddr) {
			t.Errorf("%s: got %+v err=%v", c.name, r, err)
		}
	}
}

func TestErrors(t *testing.T) {
	if _, err := New(4, 1, []string{"10.0.0.0/8", "11.0.0.0/8"}); !errors.Is(err, ErrTooManyCIDRs) {
		t.Errorf("cidr limit: %v", err)
	}
	tr, _ := New(1, 4, []string{"10.0.0.0/8"})
	if _, err := tr.Client("", "1.2.3.4, 10.0.0.1", ""); !errors.Is(err, ErrTooManyHops) {
		t.Errorf("hop limit: %v", err)
	}
	tr2, _ := New(8, 4, []string{"10.0.0.0/8"})
	_, err := tr2.Client("", "1.2.3.4, bogus, 10.0.0.1", "")
	var ae *AddrError
	if !errors.Is(err, netmatch.ErrBadAddr) || !errors.As(err, &ae) || ae.Hop != 2 {
		t.Errorf("bad addr hop: %v", err)
	}
}

func TestContainsCounter(t *testing.T) {
	cidrs := []string{"10.0.0.0/8", "192.168.0.0/16", "fd00::/8"}
	run := func(chain string) int {
		tr, _ := New(16, 3, cidrs)
		_, _ = tr.Client("", chain, "")
		return tr.contains
	}
	if got := run("1.1.1.1, 2.2.2.2, 10.0.0.1"); got > 3*3 {
		t.Errorf("contains=%d exceeds n*m=9", got)
	}
	if got := run("1.1.1.1, 2.2.2.2, 3.3.3.3"); got > 3 {
		t.Errorf("contains=%d exceeds m=3 on first-hop stop", got)
	}
}
