package ctxtmpl

import "testing"

func TestValidateURLStart(t *testing.T) {
	allowed := []string{
		"http://example.com",
		"https://example.com",
		"mailto:a@b.com",
		"/path/to",
		"#frag",
		"?q=1",
		"relative/path",
		"  /leading-space",
	}
	for _, u := range allowed {
		if err := validateURLStart(u); err != nil {
			t.Errorf("expected allow %q, got %v", u, err)
		}
	}
	blocked := []string{
		"javascript:alert(1)",
		"JaVaScRiPt:alert(1)",
		"  javascript:alert(1)",
		"java\tscript:alert(1)",
		"java\nscript:alert(1)",
		"  vbscript:msgbox",
		"data:text/html,<script>",
	}
	for _, u := range blocked {
		if err := validateURLStart(u); err != ErrUnsafeURL {
			t.Errorf("expected block %q, got %v", u, err)
		}
	}
}

func TestIsURLAttr(t *testing.T) {
	for _, a := range []string{"href", "src", "action", "formaction"} {
		if !isURLAttr(a) {
			t.Errorf("%q should be a URL attr", a)
		}
	}
	if isURLAttr("class") {
		t.Error("class should not be a URL attr")
	}
}
