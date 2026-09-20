package merge3_test

import (
	"errors"
	"slices"
	"testing"

	"ontology/internal/merge3"
)

func TestRenderConflict(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := merge3.Merge(base, []string{"a", "X", "c"}, []string{"a", "Y", "c"})
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
	if !slices.Equal(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

// An insert-versus-insert conflict has an empty base section: only the
// ||||||| marker line appears, with no content lines.
func TestRenderEmptyBaseSection(t *testing.T) {
	base := []string{"a", "b"}
	r, err := merge3.Merge(base, []string{"a", "x", "b"}, []string{"a", "y", "b"})
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
		"x",
		"||||||| base",
		"=======",
		"y",
		">>>>>>> T",
		"b",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyLabel(t *testing.T) {
	r, err := merge3.Merge([]string{"a"}, []string{"b"}, []string{"c"})
	if err != nil {
		t.Fatal(err)
	}
	for _, labels := range [][2]string{{"", "x"}, {"x", ""}, {"", ""}} {
		out, err := r.Render(labels[0], labels[1])
		if !errors.Is(err, merge3.ErrEmptyLabel) {
			t.Fatalf("Render(%q, %q): err = %v, want ErrEmptyLabel", labels[0], labels[1], err)
		}
		if out != nil {
			t.Fatalf("Render(%q, %q): out = %q, want nil", labels[0], labels[1], out)
		}
	}
}

func TestRenderCleanMerge(t *testing.T) {
	base := []string{"a", "b", "c"}
	r, err := merge3.Merge(base, []string{"a", "B", "c"}, base)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Render("ours", "theirs")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"a", "B", "c"}) {
		t.Fatalf("Render = %q", got)
	}
}
