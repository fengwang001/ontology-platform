package merge3

import (
	"errors"
	"slices"
	"testing"
)

func TestRenderDiff3Format(t *testing.T) {
	base := []string{"a", "b", "c"}
	ours := []string{"a", "O", "c"}
	theirs := []string{"a", "T", "c"}
	res := mustMerge(t, base, ours, theirs)

	got, err := res.Render("ours", "theirs")
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	want := []string{
		"a",
		"<<<<<<< ours",
		"O",
		"||||||| base",
		"b",
		"=======",
		"T",
		">>>>>>> theirs",
		"c",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyBaseSection(t *testing.T) {
	base := []string{"a"}
	ours := []string{"O", "a"}
	theirs := []string{"T", "a"}
	res := mustMerge(t, base, ours, theirs)
	if !res.HasConflict() {
		t.Fatal("expected conflict")
	}

	got, err := res.Render("me", "you")
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	want := []string{
		"<<<<<<< me",
		"O",
		"||||||| base",
		"=======",
		"T",
		">>>>>>> you",
		"a",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Render = %q, want %q", got, want)
	}
}

func TestRenderEmptyLabelError(t *testing.T) {
	res := mustMerge(t, []string{"a"}, []string{"b"}, []string{"c"})
	for _, labels := range [][2]string{{"", "x"}, {"x", ""}, {"", ""}} {
		got, err := res.Render(labels[0], labels[1])
		if !errors.Is(err, ErrEmptyLabel) {
			t.Fatalf("Render(%q, %q) err = %v, want ErrEmptyLabel", labels[0], labels[1], err)
		}
		if got != nil {
			t.Fatalf("Render(%q, %q) = %q, want nil output", labels[0], labels[1], got)
		}
	}
}

func TestRenderCleanMerge(t *testing.T) {
	base := []string{"a", "b"}
	theirs := []string{"a", "B"}
	res := mustMerge(t, base, base, theirs)
	got, err := res.Render("ours", "theirs")
	if err != nil {
		t.Fatalf("Render returned error: %v", err)
	}
	if !slices.Equal(got, theirs) {
		t.Fatalf("Render = %q, want %q", got, theirs)
	}
}
