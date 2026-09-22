package merge3

import (
	"errors"
	"reflect"
	"testing"
)

func TestRenderDiff3Format(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := Merge(base, []string{"a", "X", "c"}, []string{"a", "Y", "c"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render("ours", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a",
		"<<<<<<< ours",
		"X",
		"||||||| base",
		"b",
		"=======",
		"Y",
		">>>>>>> theirs",
		"c",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// An empty base section still renders the ||||||| marker line.
func TestRenderEmptyBaseSection(t *testing.T) {
	base := []string{"a", "b"}
	r, err := Merge(base, []string{"a", "X", "b"}, []string{"a", "Y", "b"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render("O", "T")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"a",
		"<<<<<<< O",
		"X",
		"||||||| base",
		"=======",
		"Y",
		">>>>>>> T",
		"b",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyLabel(t *testing.T) {
	r, err := Merge([]string{"a"}, []string{"b"}, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	for _, labels := range [][2]string{{"", "t"}, {"o", ""}, {"", ""}} {
		out, err := r.Render(labels[0], labels[1])
		if !errors.Is(err, ErrEmptyLabel) {
			t.Fatalf("Render(%q, %q) err = %v, want ErrEmptyLabel",
				labels[0], labels[1], err)
		}
		if out != nil {
			t.Fatalf("Render(%q, %q) returned partial output %q",
				labels[0], labels[1], out)
		}
	}
}

func TestRenderNoConflicts(t *testing.T) {
	r, err := Merge([]string{"a"}, []string{"a", "b"}, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render("ours", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("Render = %q", got)
	}
}
