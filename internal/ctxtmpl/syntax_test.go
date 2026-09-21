package ctxtmpl

import (
	"errors"
	"testing"
)

func TestRenderInterpolationSyntax(t *testing.T) {
	bad := []string{
		"{{}}",
		"{{1abc}}",
		"{{a-b}}",
		"{{abc",
		"{{ abc}}",
		"{{abc ",
		"plain text {{",
	}
	for _, tmpl := range bad {
		_, err := Render(tmpl, map[string]string{"abc": "v"})
		if !errors.Is(err, ErrInvalidInterpolation) {
			t.Errorf("Render(%q) err = %v, want ErrInvalidInterpolation", tmpl, err)
		}
	}
}

func TestRenderInterpolationInTagPosition(t *testing.T) {
	bad := []string{
		"<{{x}}>",
		"<a {{x}}=\"1\">",
		"<a {{x}}>",
		"<div {{x}}=v>",
		"<div class=v {{x}}>",
	}
	for _, tmpl := range bad {
		_, err := Render(tmpl, map[string]string{"x": "y"})
		if !errors.Is(err, ErrInterpolationInTagName) {
			t.Errorf("Render(%q) err = %v, want ErrInterpolationInTagName", tmpl, err)
		}
	}
}

func TestRenderUnclosed(t *testing.T) {
	cases := map[string]error{
		`<a href="{{x}}"`: ErrUnclosedTag,
		`<a title="{{x}}`: ErrUnclosedQuote,
		`<!-- {{x}}`:      ErrUnclosedComment,
		`<a class={{x}}`:  ErrUnclosedTag,
	}
	for tmpl, want := range cases {
		_, err := Render(tmpl, map[string]string{"x": "v"})
		if !errors.Is(err, want) {
			t.Errorf("Render(%q) err = %v, want %v", tmpl, err, want)
		}
	}
}

func TestRenderSyntaxErrorOffset(t *testing.T) {
	_, err := Render("abc {{bad", nil)
	var se *SyntaxError
	if !errors.As(err, &se) {
		t.Fatalf("got %v", err)
	}
	if se.Offset != 4 || !errors.Is(se, ErrInvalidInterpolation) {
		t.Fatalf("got offset=%d err=%v", se.Offset, se.Err)
	}
}

func TestRenderClosedTagThenText(t *testing.T) {
	got, err := Render(`<a href="/x">{{x}}</a>tail`, map[string]string{"x": "<"})
	if err != nil {
		t.Fatal(err)
	}
	want := `<a href="/x">&lt;</a>tail`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
