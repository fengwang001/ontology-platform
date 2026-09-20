package negotiate

import (
	"errors"
	"testing"
)

func selectOK(t *testing.T, accept string, offers []string) (string, string) {
	t.Helper()
	offer, rule, err := Select(accept, offers)
	if err != nil {
		t.Fatalf("Select(%q, %v) unexpected error: %v", accept, offers, err)
	}
	return offer, rule
}

// 语义 1：q 值排序，缺省为 1，支持三位小数。
func TestQValueOrdering(t *testing.T) {
	offers := []string{"text/plain", "text/html"}

	offer, rule := selectOK(t, "text/plain;q=0.5, text/html;q=0.9", offers)
	if offer != "text/html" || rule != "text/html;q=0.9" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	offer, _ = selectOK(t, "text/plain;q=0.333, text/html;q=0.332", offers)
	if offer != "text/plain" {
		t.Fatalf("three-decimal q: got %q", offer)
	}

	// 缺省 q=1 胜过显式小 q。
	offer, _ = selectOK(t, "text/plain, text/html;q=0.999", offers)
	if offer != "text/plain" {
		t.Fatalf("default q=1: got %q", offer)
	}

	offer, _ = selectOK(t, "text/html;q=1, text/plain;q=0", offers)
	if offer != "text/html" {
		t.Fatalf("q=1 vs q=0: got %q", offer)
	}
}

func TestQValueMalformed(t *testing.T) {
	offers := []string{"text/html"}
	for _, accept := range []string{
		"text/html;q=1.5",
		"text/html;q=-0.1",
		"text/html;q=abc",
		"text/html;q=0.3333",
		"text/html;q=",
		"text/html;q=1.001",
	} {
		offer, rule, err := Select(accept, offers)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("Select(%q): want ErrMalformed, got %v", accept, err)
		}
		if offer != "" || rule != "" {
			t.Fatalf("Select(%q): want empty results, got (%q, %q)", accept, offer, rule)
		}
	}
}

// 语义 2：具体度优先于 q 值。
func TestSpecificityBeatsQ(t *testing.T) {
	offers := []string{"text/html"}
	offer, rule := selectOK(t, "text/*;q=0.9, text/html;q=0.1, */*;q=1", offers)
	if offer != "text/html" || rule != "text/html;q=0.1" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	offer, rule = selectOK(t, "*/*;q=1, text/*;q=0.2", offers)
	if offer != "text/html" || rule != "text/*;q=0.2" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}
}

// 语义 3：q=0 是明确拒绝，但不影响其他 offer。
func TestQZeroRejects(t *testing.T) {
	offers := []string{"text/html", "text/plain"}
	offer, rule := selectOK(t, "text/html;q=0, */*;q=1", offers)
	if offer != "text/plain" || rule != "*/*;q=1" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	_, _, err := Select("text/html;q=0, */*;q=1", []string{"text/html"})
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("want ErrNotAcceptable, got %v", err)
	}

	_, _, err = Select("*/*;q=0", offers)
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("want ErrNotAcceptable, got %v", err)
	}
}

// 语义 4：具体度与 q 并列时按服务端顺序，且结果可复现。
func TestServerOrderTieBreak(t *testing.T) {
	offers := []string{"text/html", "text/plain"}
	for i := 0; i < 3; i++ {
		offer, rule := selectOK(t, "text/*", offers)
		if offer != "text/html" || rule != "text/*" {
			t.Fatalf("run %d: got (%q, %q)", i, offer, rule)
		}
	}

	reversed := []string{"text/plain", "text/html"}
	offer, _ := selectOK(t, "text/*;q=1, */*;q=1", reversed)
	if offer != "text/plain" {
		t.Fatalf("server order not respected: got %q", offer)
	}
}
