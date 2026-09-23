package source

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

func TestValidateKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want error
	}{
		{"simple", "a", nil},
		{"nested", "a.b.c", nil},
		{"depth100", depthKey(100), nil},
		{"empty", "", ErrEmptyKey},
		{"empty segment", "a..b", ErrEmptySegment},
		{"leading dot", ".a", ErrEmptySegment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateKey(tc.key)
			if !errors.Is(err, tc.want) {
				t.Fatalf("ValidateKey(%q) err=%v want %v", tc.key, err, tc.want)
			}
		})
	}
}

func TestNewLayerNormalizesSortedAndDedups(t *testing.T) {
	layer, err := NewLayer(Default, map[string]string{"b": "2", "a": "", "c": "3"})
	if err != nil {
		t.Fatal(err)
	}
	if layer.Level != Default {
		t.Fatalf("level=%v", layer.Level)
	}
	got := []string{}
	for _, item := range layer.Items {
		got = append(got, item.Key)
	}
	want := []string{"a", "b", "c"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("keys=%v want %v", got, want)
	}
	empty := layer.Items[0]
	if empty.Key != "a" || empty.Value != "" {
		t.Fatalf("empty value must be legal, got %+v", empty)
	}
	if _, err := NewLayer(File, map[string]string{"": "x"}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty key err=%v", err)
	}
}

func TestParseFileAndTruncation(t *testing.T) {
	full := []byte("2\ngreeting = \"hello ${name}\"\nname = \"file\"")
	layer, err := ParseFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if len(layer.Items) != 2 || layer.Items[0].Value != "hello ${name}" {
		t.Fatalf("items=%+v", layer.Items)
	}

	cases := []struct {
		name string
		at   int
		want TruncationClass
		line int
	}{
		{"header cut", 1, TruncLine, 2},
		{"first newline boundary", 2, TruncLine, 2},
		{"key cut", 5, TruncKey, 2},
		{"at equal sign", 12, TruncValue, 2},
		{"value open quote only", 12, TruncValue, 2},
		{"second newline boundary", indexOf(full, '\n', 0) + 1, TruncLine, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			class, err := ClassifyTruncation(full, tc.at)
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("err=%v want ErrTruncated", err)
			}
			if class != tc.want {
				t.Fatalf("class=%v want %v", class, tc.want)
			}
			var te *TruncationError
			if !errors.As(err, &te) || te.Line != tc.line {
				t.Fatalf("line=%v want %d: %v", te, tc.line, err)
			}
		})
	}

	counts := map[TruncationClass]int{}
	lines := map[TruncationClass]int{}
	for n := 1; n < len(full); n++ {
		class, err := ClassifyTruncation(full, n)
		if !errors.Is(err, ErrTruncated) {
			t.Fatalf("byte %d: err=%v, want truncated", n, err)
		}
		counts[class]++
		var te *TruncationError
		errors.As(err, &te)
		lines[class] = te.Line
	}
	for _, class := range []TruncationClass{TruncLine, TruncKey, TruncValue} {
		if counts[class] == 0 {
			t.Fatalf("class %v never observed over %d cuts", class, len(full)-1)
		}
	}
}

func TestEmptyFile(t *testing.T) {
	layer, err := ParseFile(nil)
	if err != nil || len(layer.Items) != 0 {
		t.Fatalf("empty file must be legal: %+v err=%v", layer, err)
	}
}

func TestEnvMappingAndAmbiguity(t *testing.T) {
	if got := flatName("a.b.c"); got != "A_B_C" {
		t.Fatalf("flatName=%s", got)
	}
	if got := escapedName("a.b.c"); got != "A__B_C" {
		t.Fatalf("escapedName=%s", got)
	}
	cases := []struct {
		name string
		env  map[string]string
		want string
		err  error
	}{
		{"flat", map[string]string{"A_B_C": "v"}, "v", nil},
		{"escaped", map[string]string{"A__B_C": "v"}, "v", nil},
		{"ambiguous", map[string]string{"A_B_C": "v1", "A__B_C": "v2"}, "", ErrAmbiguousEnv},
		{"same value not ambiguous", map[string]string{"A_B_C": "v", "A__B_C": "v"}, "v", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			layer, err := FromEnv(tc.env, []string{"a.b.c"})
			if !errors.Is(err, tc.err) {
				t.Fatalf("err=%v want %v", err, tc.err)
			}
			if tc.err == nil {
				got, ok := lookupLayer(layer, "a.b.c")
				if !ok || got != tc.want {
					t.Fatalf("value=%q ok=%v want %q", got, ok, tc.want)
				}
			}
		})
	}
}

func lookupLayer(layer Layer, key string) (string, bool) {
	for _, item := range layer.Items {
		if item.Key == key {
			return item.Value, true
		}
	}
	return "", false
}

func depthKey(depth int) string {
	parts := [][]byte{}
	for i := 0; i < depth; i++ {
		parts = append(parts, []byte("k"))
	}
	return string(bytes.Join(parts, []byte(".")))
}

func indexOf(data []byte, b byte, from int) int {
	return bytes.IndexByte(data[from:], b) + from
}
