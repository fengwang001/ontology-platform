package merge3

import (
	"errors"
	"slices"
	"testing"
)

func TestMergeIdenticalSides(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		{},
		{"a", "b", "c", "d"},
		{"x"},
		{"a", "X", "c", "Y", "z"},
		{"d", "c", "b", "a"},
	}
	for _, x := range cases {
		r := mustMerge(t, base, x, x)
		if r.HasConflict() {
			t.Fatalf("Merge(base, x, x) conflicted for %v", x)
		}
		if !slices.Equal(r.Lines, x) {
			t.Fatalf("Merge(base, x, x) = %v, want %v", r.Lines, x)
		}
	}
}

func TestMergeBaseOnOneSide(t *testing.T) {
	base := []string{"a", "b", "c", "d"}
	cases := [][]string{
		{},
		{"a", "b", "c", "d"},
		{"y"},
		{"a", "Y", "c", "Z"},
		{"n1", "n2", "a", "b", "c", "d", "n3"},
	}
	for _, y := range cases {
		r := mustMerge(t, base, base, y)
		if r.HasConflict() {
			t.Fatalf("Merge(base, base, y) conflicted for %v", y)
		}
		if !slices.Equal(r.Lines, y) {
			t.Fatalf("Merge(base, base, y) = %v, want %v", r.Lines, y)
		}
		r2 := mustMerge(t, base, y, base)
		if r2.HasConflict() || !slices.Equal(r2.Lines, y) {
			t.Fatalf("Merge(base, y, base) = %v, want %v", r2.Lines, y)
		}
	}
}

func TestRenderDiff3(t *testing.T) {
	base := []string{"head", "b", "tail"}
	ours := []string{"head", "O", "tail"}
	theirs := []string{"head", "T", "tail"}
	r := mustMerge(t, base, ours, theirs)
	out, err := r.Render("ours", "theirs")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	want := []string{
		"head",
		"<<<<<<< ours",
		"O",
		"||||||| base",
		"b",
		"=======",
		"T",
		">>>>>>> theirs",
		"tail",
	}
	if !slices.Equal(out, want) {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderEmptyBaseSection(t *testing.T) {
	base := []string{"a", "b"}
	ours := []string{"a", "O", "b"}
	theirs := []string{"a", "T", "b"}
	r := mustMerge(t, base, ours, theirs)
	out, err := r.Render("L", "R")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	want := []string{
		"a",
		"<<<<<<< L",
		"O",
		"||||||| base",
		"=======",
		"T",
		">>>>>>> R",
		"b",
	}
	if !slices.Equal(out, want) {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestRenderEmptyLabel(t *testing.T) {
	r := mustMerge(t, []string{"a"}, []string{"b"}, []string{"c"})
	for _, labels := range [][2]string{{"", "x"}, {"x", ""}, {"", ""}} {
		out, err := r.Render(labels[0], labels[1])
		if !errors.Is(err, ErrEmptyLabel) {
			t.Fatalf("Render(%q, %q) err = %v, want ErrEmptyLabel",
				labels[0], labels[1], err)
		}
		if out != nil {
			t.Fatalf("Render with empty label returned output %q", out)
		}
	}
}

func TestRenderCleanMergeHasNoMarkers(t *testing.T) {
	base := []string{"a", "b"}
	ours := []string{"a", "b", "c"}
	theirs := []string{"a", "b"}
	r := mustMerge(t, base, ours, theirs)
	out, err := r.Render("L", "R")
	if err != nil {
		t.Fatalf("Render error: %v", err)
	}
	if !slices.Equal(out, r.Lines) {
		t.Fatalf("clean render = %q, want %q", out, r.Lines)
	}
}
