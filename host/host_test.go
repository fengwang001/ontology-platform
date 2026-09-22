package host

import "testing"

func TestNormalizeBasic(t *testing.T) {
	cases := []struct {
		auth, scheme string
		host, port   string
	}{
		{"EXAMPLE.COM", "http", "example.com", ""},
		{"example.com.", "http", "example.com", ""},
		{"Example.COM..", "http", "example.com", ""},
		{"example.com:80", "http", "example.com", ""},
		{"example.com:443", "https", "example.com", ""},
		{"example.com:80", "https", "example.com", "80"}, // non-default kept
		{"example.com:8080", "http", "example.com", "8080"},
		{"example.com:080", "http", "example.com", ""}, // 080 -> 80 -> default
		{"example.com:08080", "http", "example.com", "8080"},
		{"example.com:", "http", "example.com", ""}, // empty port dropped
		{"[::1]:8080", "http", "[::1]", "8080"},
		{"[2001:db8::1]", "https", "[2001:db8::1]", ""},
		{"[2001:0db8:0000:0000:0000:0000:0000:0001]", "http", "[2001:db8::1]", ""},
		{"[2001:DB8::1]:0443", "https", "[2001:db8::1]", ""},
	}
	for _, c := range cases {
		h, p, _, err := Normalize(c.auth, c.scheme)
		if err != nil {
			t.Fatalf("Normalize(%q): %v", c.auth, err)
		}
		if h != c.host || p != c.port {
			t.Errorf("Normalize(%q,%q) = (%q,%q), want (%q,%q)",
				c.auth, c.scheme, h, p, c.host, c.port)
		}
	}
}

func TestNormalizeIdempotent(t *testing.T) {
	auths := []string{"EXAMPLE.COM.:080", "[2001:0DB8:0:0:0:0:0:1]:443", "a.b.c:099"}
	for _, a := range auths {
		h1, p1, _, err := Normalize(a, "https")
		if err != nil {
			t.Fatal(err)
		}
		rejoined := h1
		if p1 != "" {
			rejoined = h1 + ":" + p1
		}
		h2, p2, _, err := Normalize(rejoined, "https")
		if err != nil {
			t.Fatal(err)
		}
		if h1 != h2 || p1 != p2 {
			t.Errorf("not idempotent: %q -> (%q,%q) -> (%q,%q)", a, h1, p1, h2, p2)
		}
	}
}

func TestNormalizeErrors(t *testing.T) {
	bad := []string{"", "[::1", "[::1]x", "2001:db8::1", "host:abc", "host:99999", "[xyz]", "."}
	for _, b := range bad {
		if _, _, _, err := Normalize(b, "http"); err == nil {
			t.Errorf("Normalize(%q) should fail", b)
		}
	}
}

func TestChangesReported(t *testing.T) {
	_, _, ch, err := Normalize("EXAMPLE.com.:080", "http")
	if err != nil {
		t.Fatal(err)
	}
	if !ch.Case || !ch.TrailingDot || !ch.DefaultPort || !ch.PortSyntax {
		t.Errorf("expected all change flags, got %+v", ch)
	}
	_, _, ch, err = Normalize("[2001:0db8:0000:0000:0000:0000:0000:0001]", "http")
	if err != nil {
		t.Fatal(err)
	}
	if !ch.IPv6 {
		t.Errorf("expected IPv6 flag, got %+v", ch)
	}
}
