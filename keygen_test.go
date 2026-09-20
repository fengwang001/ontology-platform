package ontology

import (
	"errors"
	"strings"
	"testing"
)

func mustBetween(t *testing.T, left, right string, maxLen int) string {
	t.Helper()
	key, err := NewLexGenerator().Between(left, right, maxLen)
	if err != nil {
		t.Fatalf("Between(%q, %q) unexpected error: %v", left, right, err)
	}
	return key
}

func assertBetween(t *testing.T, left, key, right string) {
	t.Helper()
	if left != "" && !(left < key) {
		t.Errorf("key %q not greater than left %q", key, left)
	}
	if right != "" && !(key < right) {
		t.Errorf("key %q not less than right %q", key, right)
	}
	if key == "" || key[len(key)-1] == firstChar {
		t.Errorf("key %q ends with reserved %q: dead end", key, firstChar)
	}
}

func TestBetweenFront(t *testing.T) {
	key := mustBetween(t, "", "m", 0)
	assertBetween(t, "", key, "m")
}

func TestBetweenBack(t *testing.T) {
	key := mustBetween(t, "m", "", 0)
	assertBetween(t, "m", key, "")
}

func TestBetweenMiddle(t *testing.T) {
	key := mustBetween(t, "a", "c", 0)
	assertBetween(t, "a", key, "c")
	if key != "b" {
		t.Errorf("Between(%q, %q) = %q, want %q", "a", "c", key, "b")
	}
}

func TestBetweenEmptyRange(t *testing.T) {
	key := mustBetween(t, "", "", 0)
	assertBetween(t, "", key, "")
}

func TestBetweenDeterministic(t *testing.T) {
	gen := NewLexGenerator()
	want := mustBetween(t, "a1", "a2", 0)
	for i := 0; i < 100; i++ {
		got, err := gen.Between("a1", "a2", 0)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if got != want {
			t.Fatalf("call %d: got %q, want %q", i, got, want)
		}
	}
}

func TestBetweenStrictBoundsTable(t *testing.T) {
	pairs := [][2]string{
		{"", ""}, {"", "z"}, {"", "1"}, {"y", ""}, {"a", "b"},
		{"a", "a1"}, {"a1", "a2"}, {"az", "b"}, {"abc", "abd"},
		{"0z", "1"}, {"zz", "zzz"}, {"hello", "help"},
	}
	for _, p := range pairs {
		key := mustBetween(t, p[0], p[1], 0)
		assertBetween(t, p[0], key, p[1])
	}
}

func TestBetweenNeverDeadEnds(t *testing.T) {
	// Repeatedly split the lower gap: every generated key must stay
	// insertable on both sides.
	left, right := "", ""
	for i := 0; i < 50; i++ {
		key := mustBetween(t, left, right, 64)
		assertBetween(t, left, key, right)
		mustBetween(t, left, key, 64)
		mustBetween(t, key, right, 64)
		right = key
	}
}

func TestBetweenLengthLimit(t *testing.T) {
	const maxLen = 4
	left := strings.Repeat("y", maxLen)
	_, err := NewLexGenerator().Between(left, "z", maxLen)
	if !errors.Is(err, ErrNeedsRebalance) {
		t.Fatalf("got %v, want ErrNeedsRebalance", err)
	}
	if errors.Is(err, ErrInvalidOrder) {
		t.Fatal("length overflow must not be classified as invalid order")
	}
}

func TestBetweenInvalidOrder(t *testing.T) {
	gen := NewLexGenerator()
	for _, p := range [][2]string{{"b", "a"}, {"a", "a"}, {"z", "0"}} {
		_, err := gen.Between(p[0], p[1], 0)
		if !errors.Is(err, ErrInvalidOrder) {
			t.Errorf("Between(%q, %q): got %v, want ErrInvalidOrder", p[0], p[1], err)
		}
		if errors.Is(err, ErrNeedsRebalance) {
			t.Errorf("Between(%q, %q): misclassified as ErrNeedsRebalance", p[0], p[1])
		}
	}
}

func TestBetweenInvalidChar(t *testing.T) {
	gen := NewLexGenerator()
	cases := []struct {
		left, right string
		bad         byte
	}{
		{"A", "", 'A'},
		{"", "!", '!'},
		{"a b", "c", ' '},
		{"", "\xc3\xa9", '\xc3'},
	}
	for _, c := range cases {
		_, err := gen.Between(c.left, c.right, 0)
		if !errors.Is(err, ErrInvalidChar) {
			t.Errorf("Between(%q, %q): got %v, want ErrInvalidChar", c.left, c.right, err)
			continue
		}
		if !strings.Contains(err.Error(), string(c.bad)) {
			t.Errorf("Between(%q, %q): error %q does not name %q",
				c.left, c.right, err, string(c.bad))
		}
	}
}

func TestBetweenNoRoom(t *testing.T) {
	_, err := NewLexGenerator().Between("a", "a0", 0)
	if !errors.Is(err, ErrNoRoom) {
		t.Fatalf("got %v, want ErrNoRoom", err)
	}
}
