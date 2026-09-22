package host

import "testing"

func canon(t *testing.T, raw, def string) string {
	t.Helper()
	h, err := Parse(raw, def, nil)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return h.Authority()
}

func TestRegNameAndPort(t *testing.T) {
	cases := []struct {
		in, def, want string
	}{
		{"EXAMPLE.COM", "", "example.com"},
		{"Example.com.", "", "example.com"},
		{"example.com:80", "80", "example.com"},
		{"example.com:080", "80", "example.com"},
		{"example.com:443", "443", "example.com"},
		{"example.com:0443", "443", "example.com"},
		{"example.com:8080", "80", "example.com:8080"},
		{"example.com:008080", "80", "example.com:8080"},
		{"example.com:", "80", "example.com"},
	}
	for _, c := range cases {
		if got := canon(t, c.in, c.def); got != c.want {
			t.Errorf("Parse(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestIPv6(t *testing.T) {
	cases := []struct {
		in, def, want string
	}{
		{"[::1]", "", "[::1]"},
		{"[::1]:80", "80", "[::1]"},
		{"[::1]:8080", "", "[::1]:8080"},
		{"[2001:db8::1]", "", "[2001:db8::1]"},
		{"[2001:0db8:0000:0000:0000:0000:0000:0001]", "", "[2001:db8::1]"},
		{"[2001:DB8:0:0:0:0:0:1]", "", "[2001:db8::1]"},
		{"[2001:0:0:1:0:0:0:1]", "", "[2001:0:0:1::1]"},
		{"[0:0:0:0:0:0:0:1]", "", "[::1]"},
		{"[1::]", "", "[1::]"},
	}
	for _, c := range cases {
		if got := canon(t, c.in, c.def); got != c.want {
			t.Errorf("Parse(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestIPv6Equivalence(t *testing.T) {
	a := canon(t, "[2001:0db8:0000:0000:0000:0000:0000:0001]", "")
	b := canon(t, "[2001:db8::1]", "")
	if a != b {
		t.Fatalf("ipv6 forms differ: %q vs %q", a, b)
	}
}

func TestErrors(t *testing.T) {
	bad := []string{
		"[::1",
		"[gg::1]",
		"[1:2:3]",
		"[1::2::3]",
		"host:abc",
		"host:99999",
		"1:2:3",
	}
	for _, in := range bad {
		if _, err := Parse(in, "", nil); err == nil {
			t.Errorf("Parse(%q) expected error", in)
		}
	}
	_, perr := Parse("host:x", "", nil)
	if !IsErrorKind(perr, KindBadPort) {
		t.Fatal("bad port kind")
	}
}

func TestScanOnce(t *testing.T) {
	var n int
	if _, err := Parse("Example.COM:8080", "", func(k int) { n += k }); err != nil {
		t.Fatal(err)
	}
	if n != len("Example.COM:8080") {
		t.Fatalf("scanned %d", n)
	}
}
