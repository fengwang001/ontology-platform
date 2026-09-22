package host

import "testing"

func TestHostCases(t *testing.T) {
	cases := []struct {
		in, want string
		def      int
	}{
		{"Example.COM", "example.com", 0},
		{"example.com.", "example.com", 0},
		{"example.com...", "example.com", 0},
		{"example.com:8080", "example.com:8080", 0},
		{"example.com:80", "example.com", 80},
		{"example.com:443", "example.com:443", 80},
		{"example.com:080", "example.com", 80},
		{"example.com:", "example.com", 0},
		{"Example.COM:08080", "example.com:8080", 0},
		{"[::1]:8080", "[::1]:8080", 0},
		{"[::1]:80", "[::1]", 80},
		{"[2001:db8::1]", "[2001:db8::1]", 0},
		{"[2001:0db8:0000:0000:0000:0000:0000:0001]", "[2001:db8::1]", 0},
		{"[2001:DB8:0:0:0:0:0:1]", "[2001:db8::1]", 0},
		{"[0:0:0:0:0:0:0:1]", "[::1]", 0},
		{"[0:0:0:0:0:0:0:0]", "[::]", 0},
		{"[1:0:0:2:0:0:0:3]", "[1:0:0:2::3]", 0},   // longest run wins
		{"[1:0:0:1:0:0:1:0]", "[1::1:0:0:1:0]", 0}, // tie: leftmost run
	}
	for _, c := range cases {
		res, err := NormalizeWithDefault(c.in, c.def)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		got := res.Auth.Host
		if res.Auth.HasPort {
			got = res.Auth.Host + ":" + itoa(res.Auth.Port)
		}
		if got != c.want {
			t.Errorf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestIdempotent(t *testing.T) {
	for _, in := range []string{"[2001:db8::1]:80", "Example.COM:080", "host:"} {
		res, err := NormalizeWithDefault(in, 80)
		if err != nil {
			t.Fatal(err)
		}
		again, err := NormalizeWithDefault(canonical(res), 80)
		if err != nil {
			t.Fatal(err)
		}
		if canonical(res) != canonical(again) {
			t.Fatalf("not idempotent: %q -> %q -> %q", in, canonical(res), canonical(again))
		}
	}
}

func TestHostErrors(t *testing.T) {
	bad := []string{"", "[", "[::1", "[gg::1]", "[:]", "[1::2::3]", "host:abc",
		"host:99999", "[::1]x", "[]", "[::1]:"}
	lastOK := false
	for _, in := range bad {
		_, err := Normalize(in)
		if in == "[::1]:" {
			lastOK = err == nil
			continue
		}
		if err == nil {
			t.Errorf("%q: expected error", in)
		}
	}
	if !lastOK {
		t.Error("[::1]: with empty port should be accepted (port dropped)")
	}
}

func canonical(r Result) string {
	s := r.Auth.Host
	if r.Auth.HasPort {
		s += ":" + itoa(r.Auth.Port)
	}
	return s
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
