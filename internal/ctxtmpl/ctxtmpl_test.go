package ctxtmpl

import (
	"errors"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	tests := []struct {
		name    string
		tmpl    string
		data    map[string]string
		want    string
		wantErr error
	}{
		{"plain", "<p>hello</p>", nil, "<p>hello</p>", nil},
		{"multiple keys", "{{a}}{{b}}", map[string]string{"a": "1", "b": "2"}, "12", nil},
		{"text escaping", "<p>{{x}}</p>", map[string]string{"x": `<a>&`}, "<p>&lt;a&gt;&amp;</p>", nil},
		{"double quoted attr", `<a title="{{x}}">`, map[string]string{"x": `a"b&`}, `<a title="a&#34;b&amp;">`, nil},
		{"single quoted attr", `<a title='{{x}}'>`, map[string]string{"x": `a'b&`}, `<a title='a&#39;b&amp;'>`, nil},
		{"unquoted attr", "<a title={{x}}>", map[string]string{"x": `a b&`}, "<a title=a&#32;b&amp;>", nil},
		{"comment normal", "<!-- {{x}} -->", map[string]string{"x": "plain-text"}, "<!-- plain-text -->", nil},
		{"comment hyphen single", "<!-- {{x}} -->", map[string]string{"x": "a-b"}, "<!-- a-b -->", nil},
		{"javascript blocked", `<a href="{{x}}">`, map[string]string{"x": "javascript:1"}, "", ErrDangerousProtocol},
		{"vbscript blocked", `<a href="{{x}}">`, map[string]string{"x": "VBScript:x"}, "", ErrDangerousProtocol},
		{"data blocked", `<a href="{{x}}">`, map[string]string{"x": "data:text/html,x"}, "", ErrDangerousProtocol},
		{"uppercase scheme blocked", `<a href="{{x}}">`, map[string]string{"x": "JAVASCRIPT:1"}, "", ErrDangerousProtocol},
		{"leading whitespace blocked", `<a href="{{x}}">`, map[string]string{"x": "  javascript:1"}, "", ErrDangerousProtocol},
		{"embedded tab blocked", `<a href="{{x}}">`, map[string]string{"x": "java\tscript:1"}, "", ErrDangerousProtocol},
		{"embedded newline blocked", `<a href="{{x}}">`, map[string]string{"x": "java\nscript:1"}, "", ErrDangerousProtocol},
		{"embedded cr blocked", `<a href="{{x}}">`, map[string]string{"x": "java\rscript:1"}, "", ErrDangerousProtocol},
		{"https allowed", `<a href="{{x}}">`, map[string]string{"x": "https://example.com"}, `<a href="https://example.com">`, nil},
		{"relative allowed", `<a href="{{x}}">`, map[string]string{"x": "/path/to?a=1&b=2"}, `<a href="/path/to?a=1&amp;b=2">`, nil},
		{"title not guarded", `<a title="{{x}}">`, map[string]string{"x": "javascript:1"}, `<a title="javascript:1">`, nil},
		{"unquoted url guarded", `<a href={{x}}>`, map[string]string{"x": "javascript:1"}, "", ErrDangerousProtocol},
		{"single quoted url guarded", `<a href='{{x}}'>`, map[string]string{"x": "javascript:1"}, "", ErrDangerousProtocol},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.tmpl, tt.data)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Render() err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Render() unexpected err = %v", err)
			}
			if got != tt.want {
				t.Fatalf("Render() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMissingKey(t *testing.T) {
	_, err := Render("{{x}}", map[string]string{})
	if !errors.Is(err, ErrMissingKey) {
		t.Fatalf("err = %v, want ErrMissingKey", err)
	}
	if !strings.Contains(err.Error(), `"x"`) {
		t.Fatalf("err = %v, want it to name key x", err)
	}
}

func TestUnclosedAction(t *testing.T) {
	_, err := Render("<a href=\"{{x\">", map[string]string{"x": "y"})
	if !errors.Is(err, ErrUnclosedAction) {
		t.Fatalf("err = %v, want ErrUnclosedAction", err)
	}
}
