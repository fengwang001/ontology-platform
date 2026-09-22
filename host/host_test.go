package host

import "testing"

func norm(t *testing.T, in string) (string, string, bool) {
	t.Helper()
	h, p, has, err := Normalize(in)
	if err != nil {
		t.Fatalf("%s: %v", in, err)
	}
	return h, p, has
}

func TestRegNameCaseAndTrailingDot(t *testing.T) {
	h, _, has := norm(t, "ExAmPLE.COM.")
	if h != "example.com" || has {
		t.Fatalf("got %q hasPort=%v", h, has)
	}
	h, _, _ = norm(t, "example.com..")
	if h != "example.com" {
		t.Fatalf("got %q", h)
	}
}

func TestPortForms(t *testing.T) {
	_, p, has := norm(t, "example.com:080")
	if p != "80" || !has {
		t.Fatalf("got %q %v", p, has)
	}
	_, _, has = norm(t, "example.com:")
	if has {
		t.Fatal("empty port must mean no port")
	}
	_, p, _ = norm(t, "example.com:0")
	if p != "0" {
		t.Fatalf("got %q", p)
	}
}

func TestIPv6Compression(t *testing.T) {
	h1, _, _ := norm(t, "[2001:0db8:0000:0000:0000:0000:0000:0001]")
	h2, _, _ := norm(t, "[2001:db8::1]")
	if h1 != "[2001:db8::1]" || h1 != h2 {
		t.Fatalf("got %q vs %q", h1, h2)
	}
	h, p, has := norm(t, "[::1]:8080")
	if h != "[::1]" || p != "8080" || !has {
		t.Fatalf("got %q %q %v", h, p, has)
	}
}

func TestErrors(t *testing.T) {
	for _, in := range []string{"", ".", "user@h", "[::1", "a:b:c", "[1.2.3.4]", "h:x", "h:65536"} {
		if _, _, _, err := Normalize(in); err == nil {
			t.Fatalf("%q: expected error", in)
		}
	}
}

func TestIdempotent(t *testing.T) {
	h1, p1, _ := norm(t, "[2001:0DB8::1]:080")
	// Re-normalize the assembled output.
	h2, p2, has2, err := Normalize(h1 + ":" + p1)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 || p1 != p2 || !has2 {
		t.Fatalf("not idempotent: %q:%q vs %q:%q", h1, p1, h2, p2)
	}
}
