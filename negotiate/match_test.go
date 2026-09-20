package negotiate

import (
	"errors"
	"testing"
)

// 语义 5：q 之前的参数参与匹配，q 之后的参数忽略。
func TestMediaTypeParams(t *testing.T) {
	offers := []string{"text/html;level=1", "text/html"}

	offer, rule := selectOK(t, "text/html;level=1;q=0.5, text/html;q=0.4", offers)
	if offer != "text/html;level=1" || rule != "text/html;level=1;q=0.5" {
		t.Fatalf("got (%q, %q)", offer, rule)
	}

	// 带参数的规则不匹配缺参数的 offer。
	offer, _ = selectOK(t, "text/html;level=1;q=0.9, text/*;q=0.1", []string{"text/html"})
	if offer != "text/html" {
		t.Fatalf("got %q", offer)
	}

	// q 之后的参数一律忽略。
	offer, _ = selectOK(t, "text/html;q=0.5;ignored=yes", []string{"text/html"})
	if offer != "text/html" {
		t.Fatalf("params after q must be ignored: got %q", offer)
	}

	// 参数取值大小写敏感。
	_, _, err := Select("text/html;level=ONE", []string{"text/html;level=one"})
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("param values are case-sensitive: got %v", err)
	}
}

// 语义 6：空 Accept 视为 */*；offers 为空返回 ErrNotAcceptable。
func TestEmptyDefaults(t *testing.T) {
	offers := []string{"text/html", "text/plain"}
	for _, accept := range []string{"", "   ", " \t "} {
		offer, rule := selectOK(t, accept, offers)
		if offer != "text/html" || rule != "*/*" {
			t.Fatalf("accept=%q: got (%q, %q)", accept, offer, rule)
		}
	}

	_, _, err := Select("*/*", nil)
	if !errors.Is(err, ErrNotAcceptable) {
		t.Fatalf("empty offers: want ErrNotAcceptable, got %v", err)
	}
}

// 语义 7：语法错误返回 ErrMalformed 且无半结果。
func TestMalformedSyntax(t *testing.T) {
	offers := []string{"text/html"}
	for _, accept := range []string{
		"texthtml",
		"/html",
		"text/",
		"*/html",
		"text/html;level",
		"text/html;=1",
		"text/html;;q=1",
		"text /html",
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

// 语义 8：不修改入参，重复调用结果逐字节一致。
func TestNoMutationAndRepeatable(t *testing.T) {
	accept := "text/html;level=1;q=0.5, text/*;q=0.8, */*;q=0.1"
	offers := []string{"text/html;level=1", "text/plain", "image/png"}
	snapshot := append([]string(nil), offers...)

	o1, r1, err1 := Select(accept, offers)
	o2, r2, err2 := Select(accept, offers)
	if err1 != nil || err2 != nil {
		t.Fatalf("unexpected errors: %v, %v", err1, err2)
	}
	if o1 != o2 || r1 != r2 {
		t.Fatalf("not repeatable: (%q,%q) vs (%q,%q)", o1, r1, o2, r2)
	}
	if o1 != "text/plain" || r1 != "text/*;q=0.8" {
		t.Fatalf("got (%q, %q)", o1, r1)
	}
	for i := range offers {
		if offers[i] != snapshot[i] {
			t.Fatalf("offers mutated at %d: %q -> %q", i, snapshot[i], offers[i])
		}
	}
}

// 大小写不敏感的媒体类型匹配。
func TestCaseInsensitive(t *testing.T) {
	offer, _ := selectOK(t, "Text/HTML;Q=0.5", []string{"text/html"})
	if offer != "text/html" {
		t.Fatalf("got %q", offer)
	}
}
